// Ported from packages/tui/src/autocomplete.ts (pi v1.0.0).

package tui

import (
	"cmp"
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf16"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsre"
)

// autoPathDelimiters split a line into path tokens. Lookup only; not iterated.
var autoPathDelimiters = map[string]struct{}{
	" ":  {},
	"\t": {},
	`"`:  {},
	"'":  {},
	"=":  {},
}

// autoPathWrappers maps an opening wrapper to its closer. Lookup only; not iterated.
var autoPathWrappers = map[string]string{
	"(": ")",
	"[": "]",
	"{": "}",
	"<": ">",
	"`": "`",
}

// autoTokenStartRegex is tokenStartRegex: AutocompleteBoundaryRegex anchored at the end.
var autoTokenStartRegex = jsre.MustCompile(AutocompleteBoundaryRegex.Source()+"$", "u")

type autoParsedPrefix struct {
	RawPrefix      string
	IsAtPrefix     bool
	IsQuotedPrefix bool
}

type autoCompletionValueOptions struct {
	IsDirectory    bool
	IsAtPrefix     bool
	IsQuotedPrefix bool
}

type autoPathEntry struct {
	Path        string
	IsDirectory bool
}

type autoScopedFuzzyQuery struct {
	BaseDir     string
	Query       string
	DisplayBase string
}

type autoFuzzyFileOptions struct {
	IsQuotedPrefix bool
}

// AutocompleteItem is one completion row.
type AutocompleteItem struct {
	Value       string
	Label       string
	Description *string
}

// SlashCommand is a slash-command completion, optionally with argument completions.
type SlashCommand struct {
	Name         string
	Description  *string
	ArgumentHint *string
	// GetArgumentCompletions returns argument suggestions for argumentPrefix.
	// A nil slice means no argument completion is available. A non-nil empty slice is an empty result.
	GetArgumentCompletions func(argumentPrefix string) ([]AutocompleteItem, error)
}

// AutocompleteSuggestions is a completion menu for one prefix.
type AutocompleteSuggestions struct {
	Items []AutocompleteItem
	// Prefix is the text being replaced, for example "/" or "src/".
	Prefix string
}

// AutocompleteSuggestionsOptions is the options object of AutocompleteProvider.GetSuggestions.
// Cancellation is the ctx argument (TypeScript options.signal).
type AutocompleteSuggestionsOptions struct {
	// Force requests explicit completion (Tab). Nil means false.
	Force *bool
}

// AutocompleteApplyResult is the text and cursor after applying a completion.
type AutocompleteApplyResult struct {
	Lines      []string
	CursorLine int
	CursorCol  int
}

// AutocompleteCommand is a SlashCommand or an AutocompleteItem in a combined provider.
type AutocompleteCommand interface {
	isAutocompleteCommand()
}

func (*SlashCommand) isAutocompleteCommand()     {}
func (*AutocompleteItem) isAutocompleteCommand() {}

// AutocompleteProvider supplies completions for an editor.
// TriggerCharacters nil means the property is unset.
// ShouldTriggerFileCompletion is optional in TypeScript; when the method is absent, forced file
// completion proceeds. Implementations with no opinion return true.
type AutocompleteProvider interface {
	TriggerCharacters() []string
	SetTriggerCharacters(chars []string)

	// GetSuggestions returns suggestions for the cursor, or nil when none are available.
	GetSuggestions(ctx context.Context, lines []string, cursorLine int, cursorCol int, options *AutocompleteSuggestionsOptions) (*AutocompleteSuggestions, error)

	ApplyCompletion(lines []string, cursorLine int, cursorCol int, item AutocompleteItem, prefix string) AutocompleteApplyResult

	ShouldTriggerFileCompletion(lines []string, cursorLine int, cursorCol int) bool
}

// CombinedAutocompleteProvider completes slash commands and file paths.
type CombinedAutocompleteProvider struct {
	mu                sync.Mutex
	commands          []AutocompleteCommand
	basePath          string
	fdPath            *string
	triggerCharacters []string // nil means unset
}

// NewCombinedAutocompleteProvider returns a provider.
// A nil commands slice means no commands. A nil fdPath disables fd search.
func NewCombinedAutocompleteProvider(commands []AutocompleteCommand, basePath string, fdPath *string) *CombinedAutocompleteProvider {
	cmds := make([]AutocompleteCommand, len(commands))
	copy(cmds, commands)
	var fd *string
	if fdPath != nil {
		s := *fdPath
		fd = &s
	}
	return &CombinedAutocompleteProvider{
		commands: cmds,
		basePath: basePath,
		fdPath:   fd,
	}
}

// TriggerCharacters returns the natural trigger characters. Nil means unset.
func (p *CombinedAutocompleteProvider) TriggerCharacters() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.triggerCharacters == nil {
		return nil
	}
	return slices.Clone(p.triggerCharacters)
}

// SetTriggerCharacters sets the natural trigger characters. Nil clears them.
func (p *CombinedAutocompleteProvider) SetTriggerCharacters(chars []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if chars == nil {
		p.triggerCharacters = nil
		return
	}
	p.triggerCharacters = slices.Clone(chars)
}

// GetSuggestions returns slash-command, attachment, or path suggestions, or nil when none match.
func (p *CombinedAutocompleteProvider) GetSuggestions(ctx context.Context, lines []string, cursorLine int, cursorCol int, options *AutocompleteSuggestionsOptions) (*AutocompleteSuggestions, error) {
	if ctx != nil && ctx.Err() != nil {
		return nil, nil
	}
	var currentLine string
	if cursorLine >= 0 && cursorLine < len(lines) {
		currentLine = lines[cursorLine]
	}
	textBeforeCursor := js.Slice(currentLine, 0, cursorCol)

	if atPrefix := p.extractAtPrefix(textBeforeCursor); atPrefix != nil {
		parsed := autoParsePathPrefix(*atPrefix)
		suggestions, err := p.getFuzzyFileSuggestions(ctx, parsed.RawPrefix, autoFuzzyFileOptions{
			IsQuotedPrefix: parsed.IsQuotedPrefix,
		})
		if err != nil {
			return nil, err
		}
		if len(suggestions) == 0 {
			return nil, nil
		}
		return &AutocompleteSuggestions{
			Items:  suggestions,
			Prefix: *atPrefix,
		}, nil
	}

	force := options != nil && options.Force != nil && *options.Force
	commandText := js.TrimStart(textBeforeCursor)
	if !force && strings.HasPrefix(commandText, "/") {
		spaceIndex := js.IndexOf(commandText, " ", 0)
		p.mu.Lock()
		commands := slices.Clone(p.commands)
		p.mu.Unlock()

		if spaceIndex == -1 {
			prefix := js.SliceFrom(commandText, 1)
			type cmdEntry struct {
				idx         int
				name        string
				label       string
				description string
			}
			entries := make([]cmdEntry, 0, len(commands))
			for idx, cmd := range commands {
				var name, hint, desc string
				switch c := cmd.(type) {
				case *SlashCommand:
					if c == nil {
						continue
					}
					name = c.Name
					if c.ArgumentHint != nil {
						hint = *c.ArgumentHint
					}
					if c.Description != nil {
						desc = *c.Description
					}
				case *AutocompleteItem:
					if c == nil {
						continue
					}
					name = c.Value
					if c.Description != nil {
						desc = *c.Description
					}
				}
				fullDesc := desc
				if hint != "" {
					if desc != "" {
						fullDesc = hint + " — " + desc
					} else {
						fullDesc = hint
					}
				}
				entries = append(entries, cmdEntry{
					idx:         idx,
					name:        name,
					label:       name,
					description: fullDesc,
				})
			}

			bareNameMatches := FuzzyFilter(entries, prefix, func(e cmdEntry) string {
				if strings.HasPrefix(e.name, "skill:") {
					return e.name[len("skill:"):]
				}
				return e.name
			})
			bareMatchSet := make(map[int]bool, len(bareNameMatches))
			for _, m := range bareNameMatches {
				bareMatchSet[m.idx] = true
			}
			var skillOnlyCandidates []cmdEntry
			for _, e := range entries {
				if strings.HasPrefix(e.name, "skill:") && !bareMatchSet[e.idx] {
					skillOnlyCandidates = append(skillOnlyCandidates, e)
				}
			}
			fullNameOnlyMatches := FuzzyFilter(skillOnlyCandidates, prefix, func(e cmdEntry) string {
				return e.name
			})

			combined := make([]cmdEntry, 0, len(bareNameMatches)+len(fullNameOnlyMatches))
			combined = append(combined, bareNameMatches...)
			combined = append(combined, fullNameOnlyMatches...)
			if len(combined) == 0 {
				return nil, nil
			}
			filtered := make([]AutocompleteItem, 0, len(combined))
			for _, item := range combined {
				var descPtr *string
				if item.description != "" {
					d := item.description
					descPtr = &d
				}
				filtered = append(filtered, AutocompleteItem{
					Value:       item.name,
					Label:       item.label,
					Description: descPtr,
				})
			}
			return &AutocompleteSuggestions{
				Items:  filtered,
				Prefix: commandText,
			}, nil
		}

		commandName := js.Slice(commandText, 1, spaceIndex)
		argumentText := js.SliceFrom(commandText, spaceIndex+1)
		var getArgCompletions func(string) ([]AutocompleteItem, error)
		for _, cmd := range commands {
			switch c := cmd.(type) {
			case *SlashCommand:
				if c != nil && c.Name == commandName {
					getArgCompletions = c.GetArgumentCompletions
				}
			case *AutocompleteItem:
				if c != nil && c.Value == commandName {
					return nil, nil
				}
			}
			if getArgCompletions != nil {
				break
			}
		}
		if getArgCompletions == nil {
			return nil, nil
		}
		argSuggestions, err := getArgCompletions(argumentText)
		if err != nil {
			return nil, err
		}
		if len(argSuggestions) == 0 {
			return nil, nil
		}
		return &AutocompleteSuggestions{
			Items:  argSuggestions,
			Prefix: argumentText,
		}, nil
	}

	pathMatch := p.extractPathPrefix(textBeforeCursor, force)
	if pathMatch == nil {
		return nil, nil
	}
	suggestions := p.getFileSuggestions(*pathMatch)
	if len(suggestions) == 0 {
		return nil, nil
	}
	return &AutocompleteSuggestions{
		Items:  suggestions,
		Prefix: *pathMatch,
	}, nil
}

// ApplyCompletion replaces the active prefix with item and returns the new cursor.
func (p *CombinedAutocompleteProvider) ApplyCompletion(lines []string, cursorLine int, cursorCol int, item AutocompleteItem, prefix string) AutocompleteApplyResult {
	var currentLine string
	if cursorLine >= 0 && cursorLine < len(lines) {
		currentLine = lines[cursorLine]
	}
	prefixStart := cursorCol - js.Len(prefix)
	if prefixStart < 0 {
		prefixStart = 0
	}
	beforePrefix := js.Slice(currentLine, 0, prefixStart)
	afterCursor := js.SliceFrom(currentLine, cursorCol)

	isQuotedPrefix := strings.HasPrefix(prefix, `"`) || strings.HasPrefix(prefix, `@"`)
	hasLeadingQuoteAfterCursor := strings.HasPrefix(afterCursor, `"`)
	hasTrailingQuoteInItem := strings.HasSuffix(item.Value, `"`)
	adjustedAfterCursor := afterCursor
	if isQuotedPrefix && hasTrailingQuoteInItem && hasLeadingQuoteAfterCursor {
		adjustedAfterCursor = js.SliceFrom(afterCursor, 1)
	}

	newLines := slices.Clone(lines)
	if cursorLine >= len(newLines) && cursorLine >= 0 {
		for len(newLines) <= cursorLine {
			newLines = append(newLines, "")
		}
	}

	isSlashCommand := strings.HasPrefix(prefix, "/") && js.Trim(beforePrefix) == "" && !strings.Contains(js.SliceFrom(prefix, 1), "/")
	if isSlashCommand {
		newLine := beforePrefix + "/" + item.Value + " " + adjustedAfterCursor
		if cursorLine >= 0 {
			newLines[cursorLine] = newLine
		}
		return AutocompleteApplyResult{
			Lines:      newLines,
			CursorLine: cursorLine,
			CursorCol:  js.Len(beforePrefix) + js.Len(item.Value) + 2,
		}
	}

	if strings.HasPrefix(prefix, "@") {
		isDirectory := strings.HasSuffix(item.Label, "/")
		suffix := " "
		if isDirectory {
			suffix = ""
		}
		newLine := beforePrefix + item.Value + suffix + adjustedAfterCursor
		if cursorLine >= 0 {
			newLines[cursorLine] = newLine
		}
		cursorOffset := js.Len(item.Value)
		if isDirectory && hasTrailingQuoteInItem {
			cursorOffset = js.Len(item.Value) - 1
		}
		return AutocompleteApplyResult{
			Lines:      newLines,
			CursorLine: cursorLine,
			CursorCol:  js.Len(beforePrefix) + cursorOffset + js.Len(suffix),
		}
	}

	newLine := beforePrefix + item.Value + adjustedAfterCursor
	if cursorLine >= 0 {
		newLines[cursorLine] = newLine
	}
	isDirectory := strings.HasSuffix(item.Label, "/")
	cursorOffset := js.Len(item.Value)
	if isDirectory && hasTrailingQuoteInItem {
		cursorOffset = js.Len(item.Value) - 1
	}
	return AutocompleteApplyResult{
		Lines:      newLines,
		CursorLine: cursorLine,
		CursorCol:  js.Len(beforePrefix) + cursorOffset,
	}
}

// ShouldTriggerFileCompletion reports whether Tab should open file completion.
func (p *CombinedAutocompleteProvider) ShouldTriggerFileCompletion(lines []string, cursorLine int, cursorCol int) bool {
	var currentLine string
	if cursorLine >= 0 && cursorLine < len(lines) {
		currentLine = lines[cursorLine]
	}
	textBeforeCursor := js.Slice(currentLine, 0, cursorCol)
	trimmed := js.Trim(textBeforeCursor)
	if strings.HasPrefix(trimmed, "/") && !strings.Contains(trimmed, " ") {
		return false
	}
	return true
}

func (p *CombinedAutocompleteProvider) extractAtPrefix(text string) *string {
	if quotedPrefix := autoExtractQuotedPrefix(text); quotedPrefix != nil && strings.HasPrefix(*quotedPrefix, `@"`) {
		return quotedPrefix
	}
	lastDelim := autoFindLastDelimiter(text)
	rest := text
	if lastDelim != -1 {
		rest = js.SliceFrom(text, lastDelim+1)
	}
	token := autoStripLeadingWrappers(rest)
	if strings.HasPrefix(token, "@") {
		return &token
	}
	return nil
}

func (p *CombinedAutocompleteProvider) extractPathPrefix(text string, forceExtract bool) *string {
	if quotedPrefix := autoExtractQuotedPrefix(text); quotedPrefix != nil {
		return quotedPrefix
	}
	lastDelim := autoFindLastDelimiter(text)
	rest := text
	if lastDelim != -1 {
		rest = js.SliceFrom(text, lastDelim+1)
	}
	pathPrefix := autoStripLeadingWrappers(rest)

	if forceExtract {
		return &pathPrefix
	}
	if strings.Contains(pathPrefix, "/") || strings.HasPrefix(pathPrefix, ".") || strings.HasPrefix(pathPrefix, "~/") {
		return &pathPrefix
	}
	if pathPrefix == "" && text != "" && autoTokenStartRegex.Test(text) {
		return &pathPrefix
	}
	return nil
}

func (p *CombinedAutocompleteProvider) expandHomePath(pathStr string) string {
	homeDir, err := os.UserHomeDir()
	if err != nil || homeDir == "" {
		homeDir = os.Getenv("HOME")
	}
	if strings.HasPrefix(pathStr, "~/") {
		expanded := filepath.Join(homeDir, pathStr[2:])
		if strings.HasSuffix(pathStr, "/") && !strings.HasSuffix(expanded, "/") {
			return expanded + "/"
		}
		return expanded
	}
	if pathStr == "~" {
		return homeDir
	}
	return pathStr
}

func (p *CombinedAutocompleteProvider) resolveScopedFuzzyQuery(rawQuery string) *autoScopedFuzzyQuery {
	normalizedQuery := autoToDisplayPath(rawQuery)
	slashIndex := strings.LastIndex(normalizedQuery, "/")
	if slashIndex == -1 {
		return nil
	}
	displayBase := normalizedQuery[:slashIndex+1]
	query := normalizedQuery[slashIndex+1:]

	var baseDir string
	if strings.HasPrefix(displayBase, "~/") {
		baseDir = p.expandHomePath(displayBase)
	} else if strings.HasPrefix(displayBase, "/") {
		baseDir = displayBase
	} else {
		baseDir = filepath.Join(p.basePath, displayBase)
	}
	info, err := os.Stat(baseDir)
	if err != nil || !info.IsDir() {
		return nil
	}
	return &autoScopedFuzzyQuery{
		BaseDir:     baseDir,
		Query:       query,
		DisplayBase: displayBase,
	}
}

func (p *CombinedAutocompleteProvider) scopedPathForDisplay(displayBase string, relativePath string) string {
	normalizedRel := autoToDisplayPath(relativePath)
	if displayBase == "/" {
		return "/" + normalizedRel
	}
	return autoToDisplayPath(displayBase) + normalizedRel
}

func (p *CombinedAutocompleteProvider) getFileSuggestions(prefix string) []AutocompleteItem {
	parsed := autoParsePathPrefix(prefix)
	rawPrefix := parsed.RawPrefix
	expandedPrefix := rawPrefix
	if strings.HasPrefix(expandedPrefix, "~") {
		expandedPrefix = p.expandHomePath(expandedPrefix)
	}

	isRootPrefix := rawPrefix == "" ||
		rawPrefix == "./" ||
		rawPrefix == "../" ||
		rawPrefix == "~" ||
		rawPrefix == "~/" ||
		rawPrefix == "/" ||
		(parsed.IsAtPrefix && rawPrefix == "")

	var searchDir, searchPrefix string
	if isRootPrefix || strings.HasSuffix(rawPrefix, "/") {
		if strings.HasPrefix(rawPrefix, "~") || strings.HasPrefix(expandedPrefix, "/") {
			searchDir = expandedPrefix
		} else {
			searchDir = filepath.Join(p.basePath, expandedPrefix)
		}
		searchPrefix = ""
	} else {
		dir := filepath.Dir(expandedPrefix)
		file := filepath.Base(expandedPrefix)
		if strings.HasPrefix(rawPrefix, "~") || strings.HasPrefix(expandedPrefix, "/") {
			searchDir = dir
		} else {
			searchDir = filepath.Join(p.basePath, dir)
		}
		searchPrefix = file
	}

	entries, err := os.ReadDir(searchDir)
	if err != nil {
		return nil
	}

	lowerSearchPrefix := strings.ToLower(searchPrefix)
	suggestions := make([]AutocompleteItem, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(strings.ToLower(name), lowerSearchPrefix) {
			continue
		}

		isDirectory := entry.IsDir()
		if !isDirectory && entry.Type()&fs.ModeSymlink != 0 {
			if info, err := os.Stat(filepath.Join(searchDir, name)); err == nil && info.IsDir() {
				isDirectory = true
			}
		}

		var relativePath string
		displayPrefix := rawPrefix
		if strings.HasSuffix(displayPrefix, "/") {
			relativePath = displayPrefix + name
		} else if strings.ContainsAny(displayPrefix, `/\`) {
			if strings.HasPrefix(displayPrefix, "~/") {
				homeRelDir := displayPrefix[2:]
				dir := filepath.Dir(homeRelDir)
				if dir == "." {
					relativePath = "~/" + name
				} else {
					relativePath = "~/" + filepath.Join(dir, name)
				}
			} else if strings.HasPrefix(displayPrefix, "/") {
				dir := filepath.Dir(displayPrefix)
				if dir == "/" {
					relativePath = "/" + name
				} else {
					relativePath = dir + "/" + name
				}
			} else {
				relativePath = filepath.Join(filepath.Dir(displayPrefix), name)
				if strings.HasPrefix(displayPrefix, "./") && !strings.HasPrefix(relativePath, "./") {
					relativePath = "./" + relativePath
				}
			}
		} else {
			if strings.HasPrefix(displayPrefix, "~") {
				relativePath = "~/" + name
			} else {
				relativePath = name
			}
		}

		relativePath = autoToDisplayPath(relativePath)
		pathValue := relativePath
		label := name
		if isDirectory {
			pathValue += "/"
			label += "/"
		}
		value := autoBuildCompletionValue(pathValue, autoCompletionValueOptions{
			IsDirectory:    isDirectory,
			IsAtPrefix:     parsed.IsAtPrefix,
			IsQuotedPrefix: parsed.IsQuotedPrefix,
		})
		suggestions = append(suggestions, AutocompleteItem{
			Value: value,
			Label: label,
		})
	}

	slices.SortFunc(suggestions, func(a, b AutocompleteItem) int {
		aIsDir := strings.HasSuffix(a.Label, "/")
		bIsDir := strings.HasSuffix(b.Label, "/")
		if aIsDir != bIsDir {
			if aIsDir {
				return -1
			}
			return 1
		}
		return autoCompareStrings(a.Label, b.Label)
	})
	return suggestions
}

func (p *CombinedAutocompleteProvider) scoreEntry(filePath string, query string, isDirectory bool) int {
	fileName := filepath.Base(filePath)
	lowerFileName := strings.ToLower(fileName)
	lowerQuery := strings.ToLower(query)

	score := 0
	if lowerFileName == lowerQuery {
		score = 100
	} else if strings.HasPrefix(lowerFileName, lowerQuery) {
		score = 80
	} else if strings.Contains(lowerFileName, lowerQuery) {
		score = 50
	} else if strings.Contains(strings.ToLower(filePath), lowerQuery) {
		score = 30
	}

	if isDirectory && score > 0 {
		score += 10
	}
	return score
}

func (p *CombinedAutocompleteProvider) getBaseDirSuggestions(ctx context.Context, baseDir string, query string) ([]autoPathEntry, error) {
	if p.fdPath == nil || *p.fdPath == "" || (ctx != nil && ctx.Err() != nil) {
		return nil, nil
	}
	depth := 1
	return autoWalkDirectoryWithFd(ctx, baseDir, *p.fdPath, query, 100, &depth)
}

func (p *CombinedAutocompleteProvider) getFuzzyFileSuggestions(ctx context.Context, query string, options autoFuzzyFileOptions) ([]AutocompleteItem, error) {
	if p.fdPath == nil || *p.fdPath == "" || (ctx != nil && ctx.Err() != nil) {
		return nil, nil
	}

	scopedQuery := p.resolveScopedFuzzyQuery(query)
	fdBaseDir := p.basePath
	fdQuery := query
	if scopedQuery != nil {
		fdBaseDir = scopedQuery.BaseDir
		fdQuery = scopedQuery.Query
	}

	baseDirEntries, err := p.getBaseDirSuggestions(ctx, fdBaseDir, fdQuery)
	if err != nil {
		return nil, err
	}
	recursiveEntries, err := autoWalkDirectoryWithFd(ctx, fdBaseDir, *p.fdPath, fdQuery, 100, nil)
	if err != nil {
		return nil, err
	}
	if ctx != nil && ctx.Err() != nil {
		return nil, nil
	}

	seenPaths := make(map[string]bool, len(baseDirEntries))
	entries := make([]autoPathEntry, 0, len(baseDirEntries)+len(recursiveEntries))
	for _, entry := range baseDirEntries {
		if !seenPaths[entry.Path] {
			seenPaths[entry.Path] = true
			entries = append(entries, entry)
		}
	}
	for _, entry := range recursiveEntries {
		if !seenPaths[entry.Path] {
			seenPaths[entry.Path] = true
			entries = append(entries, entry)
		}
	}

	type scoredEntry struct {
		autoPathEntry
		score int
		depth int
	}
	scoredEntries := make([]scoredEntry, 0, len(entries))
	for _, entry := range entries {
		score := 1
		if fdQuery != "" {
			score = p.scoreEntry(entry.Path, fdQuery, entry.IsDirectory)
		}
		if score <= 0 {
			continue
		}
		depth := 0
		for _, part := range strings.Split(autoToDisplayPath(entry.Path), "/") {
			if part != "" {
				depth++
			}
		}
		scoredEntries = append(scoredEntries, scoredEntry{
			autoPathEntry: entry,
			score:         score,
			depth:         depth,
		})
	}

	slices.SortFunc(scoredEntries, func(a, b scoredEntry) int {
		if c := cmp.Compare(b.score, a.score); c != 0 {
			return c
		}
		if c := cmp.Compare(a.depth, b.depth); c != 0 {
			return c
		}
		if c := cmp.Compare(len(a.Path), len(b.Path)); c != 0 {
			return c
		}
		return autoCompareStrings(a.Path, b.Path)
	})
	if len(scoredEntries) > 20 {
		scoredEntries = scoredEntries[:20]
	}

	suggestions := make([]AutocompleteItem, 0, len(scoredEntries))
	for _, entry := range scoredEntries {
		pathWithoutSlash := entry.Path
		if entry.IsDirectory && strings.HasSuffix(pathWithoutSlash, "/") {
			pathWithoutSlash = pathWithoutSlash[:len(pathWithoutSlash)-1]
		}
		displayPath := pathWithoutSlash
		if scopedQuery != nil {
			displayPath = p.scopedPathForDisplay(scopedQuery.DisplayBase, pathWithoutSlash)
		}
		entryName := filepath.Base(pathWithoutSlash)
		completionPath := displayPath
		label := entryName
		if entry.IsDirectory {
			completionPath += "/"
			label += "/"
		}
		value := autoBuildCompletionValue(completionPath, autoCompletionValueOptions{
			IsDirectory:    entry.IsDirectory,
			IsAtPrefix:     true,
			IsQuotedPrefix: options.IsQuotedPrefix,
		})
		dp := displayPath
		suggestions = append(suggestions, AutocompleteItem{
			Value:       value,
			Label:       label,
			Description: &dp,
		})
	}
	return suggestions, nil
}

func autoCompareStrings(a, b string) int {
	if c := cmp.Compare(strings.ToLower(a), strings.ToLower(b)); c != 0 {
		return c
	}
	if a == b {
		return 0
	}
	// Match en-US localeCompare where lowercase sorts before uppercase on case-folded ties.
	if a > b {
		return -1
	}
	return 1
}

func autoToDisplayPath(value string) string {
	return strings.ReplaceAll(value, "\\", "/")
}

var autoEscapeRegexReplacer = strings.NewReplacer(
	`\`, `\\`,
	".", `\.`,
	"*", `\*`,
	"+", `\+`,
	"?", `\?`,
	"^", `\^`,
	"$", `\$`,
	"{", `\{`,
	"}", `\}`,
	"(", `\(`,
	")", `\)`,
	"|", `\|`,
	"[", `\[`,
	"]", `\]`,
)

func autoEscapeRegex(value string) string {
	return autoEscapeRegexReplacer.Replace(value)
}

func autoBuildFdPathQuery(query string) string {
	normalized := autoToDisplayPath(query)
	if !strings.Contains(normalized, "/") {
		return autoEscapeRegex(normalized)
	}

	hasTrailingSeparator := strings.HasSuffix(normalized, "/")
	trimmed := strings.Trim(normalized, "/")
	if trimmed == "" {
		return normalized
	}

	const separatorPattern = `[\\/]`
	parts := strings.Split(trimmed, "/")
	segments := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			segments = append(segments, autoEscapeRegex(part))
		}
	}

	pattern := strings.Join(segments, separatorPattern)
	if hasTrailingSeparator {
		pattern += separatorPattern
	}
	return pattern
}

func autoFindLastDelimiter(text string) int {
	lastDelimiter := -1
	index := 0
	for _, r := range text {
		ch := string(r)
		index += utf16.RuneLen(r)
		if _, ok := autoPathDelimiters[ch]; ok || AutocompleteSeparatorRegex.Test(ch) {
			lastDelimiter = index - 1
		}
	}
	return lastDelimiter
}

func autoStripLeadingWrappers(token string) string {
	result := token
	for len(result) > 0 {
		first := result[:1]
		closer, ok := autoPathWrappers[first]
		if !ok || strings.Contains(result[1:], closer) {
			break
		}
		result = result[1:]
	}
	return result
}

func autoFindUnclosedQuoteStart(text string) *int {
	inQuotes := false
	quoteStart := -1
	pos := 0
	for _, r := range text {
		if r == '"' {
			inQuotes = !inQuotes
			if inQuotes {
				quoteStart = pos
			}
		}
		pos += utf16.RuneLen(r)
	}
	if !inQuotes {
		return nil
	}
	return &quoteStart
}

func autoIsTokenStart(text string, index int) bool {
	start := index
	for start > 0 {
		prev := js.Slice(text, start-1, start)
		if _, ok := autoPathWrappers[prev]; !ok {
			break
		}
		start--
	}
	prev := ""
	if start > 0 {
		prev = js.Slice(text, start-1, start)
	}
	if _, ok := autoPathDelimiters[prev]; ok {
		return true
	}
	return autoTokenStartRegex.Test(js.Slice(text, 0, start))
}

func autoExtractQuotedPrefix(text string) *string {
	quoteStart := autoFindUnclosedQuoteStart(text)
	if quoteStart == nil {
		return nil
	}
	qs := *quoteStart
	if qs > 0 && js.Slice(text, qs-1, qs) == "@" {
		if !autoIsTokenStart(text, qs-1) {
			return nil
		}
		res := js.SliceFrom(text, qs-1)
		return &res
	}
	if !autoIsTokenStart(text, qs) {
		return nil
	}
	res := js.SliceFrom(text, qs)
	return &res
}

func autoParsePathPrefix(prefix string) autoParsedPrefix {
	if strings.HasPrefix(prefix, `@"`) {
		return autoParsedPrefix{RawPrefix: prefix[2:], IsAtPrefix: true, IsQuotedPrefix: true}
	}
	if strings.HasPrefix(prefix, `"`) {
		return autoParsedPrefix{RawPrefix: prefix[1:], IsAtPrefix: false, IsQuotedPrefix: true}
	}
	if strings.HasPrefix(prefix, "@") {
		return autoParsedPrefix{RawPrefix: prefix[1:], IsAtPrefix: true, IsQuotedPrefix: false}
	}
	return autoParsedPrefix{RawPrefix: prefix, IsAtPrefix: false, IsQuotedPrefix: false}
}

func autoBuildCompletionValue(pathStr string, options autoCompletionValueOptions) string {
	needsQuotes := options.IsQuotedPrefix || AutocompleteSeparatorRegex.Test(pathStr)
	prefix := ""
	if options.IsAtPrefix {
		prefix = "@"
	}
	if !needsQuotes {
		return prefix + pathStr
	}
	return prefix + `"` + pathStr + `"`
}

func autoWalkDirectoryWithFd(ctx context.Context, baseDir string, fdPath string, query string, maxResults int, maxDepth *int) ([]autoPathEntry, error) {
	if ctx != nil && ctx.Err() != nil {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	args := []string{
		"--base-directory", baseDir,
		"--max-results", strconv.Itoa(maxResults),
		"--type", "f",
		"--type", "d",
		"--follow",
		"--hidden",
		"--exclude", ".git",
		"--exclude", ".git/*",
		"--exclude", ".git/**",
	}
	if maxDepth != nil {
		args = append(args, "--max-depth", strconv.Itoa(*maxDepth))
	}
	if strings.Contains(autoToDisplayPath(query), "/") {
		args = append(args, "--full-path")
	}
	if query != "" {
		args = append(args, autoBuildFdPathQuery(query))
	}

	cmd := exec.CommandContext(ctx, fdPath, args...)
	out, err := cmd.Output()
	if ctx.Err() != nil || err != nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return nil, nil
	}

	lines := strings.Split(trimmed, "\n")
	results := make([]autoPathEntry, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		displayLine := autoToDisplayPath(line)
		hasTrailingSeparator := strings.HasSuffix(displayLine, "/")
		normalizedPath := displayLine
		if hasTrailingSeparator {
			normalizedPath = displayLine[:len(displayLine)-1]
		}
		if normalizedPath == ".git" || strings.HasPrefix(normalizedPath, ".git/") || strings.Contains(normalizedPath, "/.git/") {
			continue
		}
		results = append(results, autoPathEntry{
			Path:        displayLine,
			IsDirectory: hasTrailingSeparator,
		})
	}
	return results, nil
}

var _ AutocompleteProvider = (*CombinedAutocompleteProvider)(nil)
