package gui

import (
	"context"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
)

// FuzzyResult represents the outcome of matching a query against candidate text.
// Lower scores indicate stronger matches (0 is exact / empty query).
type FuzzyResult struct {
	Matches bool
	Score   float64
}

func isWordBoundaryRune(r rune) bool {
	return unicode.IsSpace(r) || r == '-' || r == '_' || r == '.' || r == '/' || r == ':'
}

func isAlphaDigitSwapPair(a, b rune) bool {
	return (unicode.IsLetter(a) && unicode.IsDigit(b)) || (unicode.IsDigit(a) && unicode.IsLetter(b))
}

func matchSingleToken(queryRunes, textRunes []rune) FuzzyResult {
	if len(queryRunes) == 0 {
		return FuzzyResult{Matches: true, Score: 0}
	}
	if len(queryRunes) > len(textRunes) {
		return FuzzyResult{Matches: false, Score: 0}
	}

	match := func(q []rune) FuzzyResult {
		qi := 0
		score := 0.0
		lastMatch := -1
		consecutive := 0

		for ti := 0; ti < len(textRunes) && qi < len(q); ti++ {
			if textRunes[ti] != q[qi] {
				continue
			}
			isBoundary := ti == 0 || isWordBoundaryRune(textRunes[ti-1])
			if lastMatch == ti-1 {
				consecutive++
				score -= float64(consecutive) * 5.0
			} else {
				consecutive = 0
				if lastMatch >= 0 {
					score += float64(ti-lastMatch-1) * 0.5
				}
			}
			if isBoundary {
				score -= 10.0
			}
			score += float64(ti) * 0.1
			lastMatch = ti
			qi++
		}

		if qi < len(q) {
			return FuzzyResult{Matches: false, Score: 0}
		}
		score += float64(len(textRunes)-len(q)) * 0.1
		return FuzzyResult{Matches: true, Score: score}
	}

	primary := match(queryRunes)
	if primary.Matches {
		return primary
	}

	// Single adjacent alphanumeric swap tolerance (e.g. "1a" <-> "a1").
	for i := 0; i < len(queryRunes)-1; i++ {
		if !isAlphaDigitSwapPair(queryRunes[i], queryRunes[i+1]) {
			continue
		}
		swapped := make([]rune, len(queryRunes))
		copy(swapped, queryRunes)
		swapped[i], swapped[i+1] = swapped[i+1], swapped[i]
		res := match(swapped)
		if res.Matches {
			res.Score += 5.0
			return res
		}
	}
	return FuzzyResult{Matches: false, Score: 0}
}

// FuzzyMatch matches query against text using pi's boundary-weighted fuzzy
// scoring algorithm.
func FuzzyMatch(query, text string) FuzzyResult {
	q := strings.TrimSpace(strings.ToLower(query))
	if q == "" {
		return FuzzyResult{Matches: true, Score: 0}
	}
	tRunes := []rune(strings.ToLower(text))
	tokens := strings.Fields(q)
	if len(tokens) == 0 {
		return FuzzyResult{Matches: true, Score: 0}
	}

	totalScore := 0.0
	for _, tok := range tokens {
		res := matchSingleToken([]rune(tok), tRunes)
		if !res.Matches {
			return FuzzyResult{Matches: false, Score: 0}
		}
		totalScore += res.Score
	}
	return FuzzyResult{Matches: true, Score: totalScore}
}

// FuzzyFilter filters and sorts items by descending FuzzyMatch quality.
func FuzzyFilter[T any](items []T, query string, getText func(T) string) []T {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		out := make([]T, len(items))
		copy(out, items)
		return out
	}

	type scored struct {
		item  T
		score float64
		idx   int
	}
	var matches []scored
	for i, it := range items {
		res := FuzzyMatch(trimmed, getText(it))
		if res.Matches {
			matches = append(matches, scored{item: it, score: res.Score, idx: i})
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score < matches[j].score
		}
		return matches[i].idx < matches[j].idx
	})
	out := make([]T, len(matches))
	for i, m := range matches {
		out[i] = m.item
	}
	return out
}

// AutocompleteItem represents a single selectable suggestion in the popup list.
type AutocompleteItem struct {
	Value       string
	Label       string
	Description string
}

// SlashCommand registers a slash command and optional argument completion provider.
type SlashCommand struct {
	Name                   string
	Description            string
	GetArgumentCompletions func(argumentPrefix string) []AutocompleteItem
}

// AutocompleteSuggestions holds the candidate items and matched prefix token.
type AutocompleteSuggestions struct {
	Items  []AutocompleteItem
	Prefix string
}

// AutocompleteApplyResult holds the updated editor lines and cursor coordinate
// (line index and rune offset within line) after applying a completion item.
type AutocompleteApplyResult struct {
	Lines      []string
	CursorLine int
	CursorCol  int
}

// AutocompleteProvider supplies completions for Editor instances.
type AutocompleteProvider interface {
	TriggerCharacters() []string
	GetSuggestions(ctx context.Context, lines []string, cursorLine, cursorCol int, force bool) (*AutocompleteSuggestions, error)
	ApplyCompletion(lines []string, cursorLine, cursorCol int, item AutocompleteItem, prefix string) AutocompleteApplyResult
	ShouldTriggerFileCompletion(lines []string, cursorLine, cursorCol int) bool
}

// CombinedAutocompleteProvider provides fuzzy `/` slash command completion,
// command argument completion, and `@file` / relative path completion against a
// base directory.
type CombinedAutocompleteProvider struct {
	mu                sync.RWMutex
	commands          []SlashCommand
	basePath          string
	triggerCharacters []string
	homeDirFn         func() (string, error)
	maxWalkEntries    int
}

// NewCombinedAutocompleteProvider creates a provider rooted at basePath.
func NewCombinedAutocompleteProvider(commands []SlashCommand, basePath string) *CombinedAutocompleteProvider {
	cmdsCopy := make([]SlashCommand, len(commands))
	copy(cmdsCopy, commands)
	return &CombinedAutocompleteProvider{
		commands:          cmdsCopy,
		basePath:          basePath,
		triggerCharacters: []string{"@"},
		homeDirFn:         os.UserHomeDir,
		maxWalkEntries:    2000,
	}
}

// TriggerCharacters returns the trigger runes configured for auto-opening completion.
func (p *CombinedAutocompleteProvider) TriggerCharacters() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]string, len(p.triggerCharacters))
	copy(out, p.triggerCharacters)
	return out
}

// SetTriggerCharacters overrides auto-trigger characters (default: ["@"]).
func (p *CombinedAutocompleteProvider) SetTriggerCharacters(chars []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.triggerCharacters = append([]string(nil), chars...)
}

// SetCommands replaces the slash commands offered by this provider.
func (p *CombinedAutocompleteProvider) SetCommands(commands []SlashCommand) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.commands = append([]SlashCommand(nil), commands...)
}

// SetBasePath updates the workspace base directory for file completions.
func (p *CombinedAutocompleteProvider) SetBasePath(basePath string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.basePath = basePath
}

// ShouldTriggerFileCompletion reports whether Tab should trigger file completion
// at (cursorLine, cursorCol) (cols in runes).
func (p *CombinedAutocompleteProvider) ShouldTriggerFileCompletion(lines []string, cursorLine, cursorCol int) bool {
	before := textBeforeCursor(lines, cursorLine, cursorCol)
	trimmed := strings.TrimSpace(before)
	if strings.HasPrefix(trimmed, "/") && !strings.Contains(trimmed, " ") {
		return false
	}
	return true
}

// GetSuggestions computes slash command, command argument, or `@file`/path
// completions for the text preceding the cursor (cursorCol in runes).
func (p *CombinedAutocompleteProvider) GetSuggestions(
	ctx context.Context,
	lines []string,
	cursorLine, cursorCol int,
	force bool,
) (*AutocompleteSuggestions, error) {
	if ctx != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}

	p.mu.RLock()
	commands := append([]SlashCommand(nil), p.commands...)
	basePath := p.basePath
	maxWalk := p.maxWalkEntries
	homeFn := p.homeDirFn
	p.mu.RUnlock()

	if basePath == "" {
		if wd, err := os.Getwd(); err == nil {
			basePath = wd
		}
	}

	before := textBeforeCursor(lines, cursorLine, cursorCol)

	// 1. Check @file attachment trigger anywhere in line.
	if atPrefix, quoted, ok := extractAtPrefix(before); ok {
		rawQuery := strings.TrimPrefix(atPrefix, "@")
		if quoted {
			rawQuery = strings.TrimPrefix(rawQuery, `"`)
		}
		items := p.fuzzyFileSuggestions(ctx, basePath, rawQuery, quoted, maxWalk, homeFn)
		if len(items) == 0 {
			return nil, nil
		}
		return &AutocompleteSuggestions{
			Items:  items,
			Prefix: atPrefix,
		}, nil
	}

	// 2. Check leading `/` slash command / argument completion on line 0 or single-line start.
	if cursorLine == 0 && strings.HasPrefix(before, "/") {
		spaceIdx := strings.IndexByte(before, ' ')
		if spaceIdx == -1 {
			prefix := before
			query := before[1:]
			items := make([]AutocompleteItem, 0, len(commands))
			for _, cmd := range commands {
				items = append(items, AutocompleteItem{
					Value:       cmd.Name,
					Label:       cmd.Name,
					Description: cmd.Description,
				})
			}
			filtered := FuzzyFilter(items, query, func(it AutocompleteItem) string {
				return it.Label
			})
			if len(filtered) == 0 {
				return nil, nil
			}
			return &AutocompleteSuggestions{
				Items:  filtered,
				Prefix: prefix,
			}, nil
		}

		cmdName := before[1:spaceIdx]
		argText := before[spaceIdx+1:]
		for _, cmd := range commands {
			if cmd.Name == cmdName && cmd.GetArgumentCompletions != nil {
				args := cmd.GetArgumentCompletions(argText)
				if len(args) == 0 {
					return nil, nil
				}
				return &AutocompleteSuggestions{
					Items:  args,
					Prefix: argText,
				}, nil
			}
		}
		return nil, nil
	}

	// 3. Explicit Tab / forced path completion.
	if !force {
		return nil, nil
	}
	if pathPrefix, quoted, ok := extractPathPrefix(before, true); ok {
		items := p.dirPathSuggestions(ctx, basePath, pathPrefix, quoted, homeFn)
		if len(items) == 0 {
			return nil, nil
		}
		return &AutocompleteSuggestions{
			Items:  items,
			Prefix: pathPrefix,
		}, nil
	}
	return nil, nil
}

// ApplyCompletion replaces prefix ending at (cursorLine, cursorCol) with item.Value.
func (p *CombinedAutocompleteProvider) ApplyCompletion(
	lines []string,
	cursorLine, cursorCol int,
	item AutocompleteItem,
	prefix string,
) AutocompleteApplyResult {
	outLines := make([]string, len(lines))
	copy(outLines, lines)
	if cursorLine < 0 || cursorLine >= len(outLines) {
		return AutocompleteApplyResult{Lines: outLines, CursorLine: cursorLine, CursorCol: cursorCol}
	}

	lineRunes := []rune(outLines[cursorLine])
	if cursorCol < 0 {
		cursorCol = 0
	}
	if cursorCol > len(lineRunes) {
		cursorCol = len(lineRunes)
	}
	prefixRunes := utf8.RuneCountInString(prefix)

	// Slash command insertion at start of line adds a trailing space unless it ends in space.
	replacement := item.Value
	isSlashCmd := strings.HasPrefix(prefix, "/") && !strings.Contains(prefix, " ")
	if isSlashCmd && !strings.HasPrefix(replacement, "/") {
		replacement = "/" + replacement
	}
	if isSlashCmd && !strings.HasSuffix(replacement, " ") {
		replacement += " "
	} else if strings.HasPrefix(prefix, "@") && !strings.HasSuffix(replacement, "/") && !strings.HasSuffix(replacement, " ") {
		replacement += " "
	}

	startCol := cursorCol - prefixRunes
	if startCol < 0 {
		startCol = 0
	}
	repRunes := []rune(replacement)
	updated := make([]rune, 0, len(lineRunes)-prefixRunes+len(repRunes))
	updated = append(updated, lineRunes[:startCol]...)
	updated = append(updated, repRunes...)
	updated = append(updated, lineRunes[cursorCol:]...)

	outLines[cursorLine] = string(updated)
	return AutocompleteApplyResult{
		Lines:      outLines,
		CursorLine: cursorLine,
		CursorCol:  startCol + len(repRunes),
	}
}

func textBeforeCursor(lines []string, cursorLine, cursorCol int) string {
	if cursorLine < 0 || cursorLine >= len(lines) {
		return ""
	}
	r := []rune(lines[cursorLine])
	if cursorCol < 0 {
		cursorCol = 0
	}
	if cursorCol > len(r) {
		cursorCol = len(r)
	}
	return string(r[:cursorCol])
}

func isAtBoundary(ch rune) bool {
	return unicode.IsSpace(ch) || ch == '(' || ch == '[' || ch == '{' || ch == '<' || ch == '`' || ch == '\''
}

func isUnquotedSeparator(ch rune) bool {
	return unicode.IsSpace(ch) || ch == ',' || ch == ';' || ch == ')' || ch == ']' || ch == '}' || ch == '>'
}

// extractAtPrefix parses a trailing @token or @"quoted path" immediately before cursor.
func extractAtPrefix(before string) (prefix string, quoted bool, ok bool) {
	runes := []rune(before)
	for i := len(runes) - 1; i >= 0; i-- {
		if runes[i] == '@' {
			if i > 0 && !isAtBoundary(runes[i-1]) {
				return "", false, false
			}
			tail := runes[i+1:]
			if len(tail) > 0 && tail[0] == '"' {
				// Quoted path: allow spaces, forbid second quote unless unmatched.
				for j := 1; j < len(tail); j++ {
					if tail[j] == '"' {
						return "", false, false
					}
				}
				return string(runes[i:]), true, true
			}
			for _, ch := range tail {
				if isUnquotedSeparator(ch) {
					return "", false, false
				}
			}
			return string(runes[i:]), false, true
		}
		// Keep scanning backwards if still inside possible quoted path, else stop at space.
		if runes[i] == '"' {
			// Continue to find '@' if immediately preceding quote, e.g. @"foo bar
			continue
		}
		if isUnquotedSeparator(runes[i]) && !hasOpenAtQuote(runes[:i+1]) {
			break
		}
	}
	return "", false, false
}

func hasOpenAtQuote(runes []rune) bool {
	for i := len(runes) - 1; i >= 1; i-- {
		if runes[i] == '"' && runes[i-1] == '@' {
			if i-1 == 0 || isAtBoundary(runes[i-2]) {
				return true
			}
		}
	}
	return false
}

func extractPathPrefix(before string, force bool) (prefix string, quoted bool, ok bool) {
	if !force && !strings.ContainsAny(before, "/\\.") {
		return "", false, false
	}
	runes := []rune(before)
	start := len(runes)
	for i := len(runes) - 1; i >= 0; i-- {
		if isUnquotedSeparator(runes[i]) {
			break
		}
		start = i
	}
	token := string(runes[start:])
	if token == "" && !force {
		return "", false, false
	}
	if strings.HasPrefix(token, `"`) {
		return token, true, true
	}
	return token, false, true
}

func formatAtCompletionValue(displayPath string, isDir, atPrefix, quoted bool) string {
	needsQuotes := quoted || strings.ContainsAny(displayPath, " \t")
	p := strings.TrimSuffix(displayPath, "/")
	if isDir {
		p += "/"
	}
	prefix := ""
	if atPrefix {
		prefix = "@"
	}
	if needsQuotes {
		if isDir {
			return prefix + `"` + p
		}
		return prefix + `"` + p + `"`
	}
	return prefix + p
}

func resolveSearchScope(basePath, rawQuery string, homeFn func() (string, error)) (searchBase, displayBase, subQuery string) {
	if strings.HasPrefix(rawQuery, "~/") || rawQuery == "~" {
		if homeFn != nil {
			if home, err := homeFn(); err == nil && home != "" {
				rest := strings.TrimPrefix(rawQuery, "~")
				rest = strings.TrimPrefix(rest, "/")
				return home, "~", rest
			}
		}
	}
	if strings.HasPrefix(rawQuery, "./") {
		return basePath, ".", strings.TrimPrefix(rawQuery, "./")
	}
	if strings.HasPrefix(rawQuery, "../") {
		dir := filepath.Dir(basePath)
		return dir, "..", strings.TrimPrefix(rawQuery, "../")
	}
	return basePath, "", rawQuery
}

func joinDisplayPath(displayBase, rel string) string {
	slashRel := filepath.ToSlash(rel)
	if displayBase == "" {
		return slashRel
	}
	if slashRel == "" {
		return displayBase
	}
	return path.Join(displayBase, slashRel)
}

func shouldSkipWalkDir(name string) bool {
	switch name {
	case ".git", "node_modules", ".hg", ".svn", ".DS_Store", ".idea", ".vscode":
		return true
	}
	return false
}

func scoreFileEntry(relPath, query string, isDir bool) (float64, bool) {
	if query == "" {
		if isDir {
			return 20, true
		}
		return 10, true
	}
	base := path.Base(strings.TrimSuffix(relPath, "/"))
	lowerBase := strings.ToLower(base)
	lowerPath := strings.ToLower(relPath)
	lowerQuery := strings.ToLower(query)

	var score float64
	matched := false
	switch {
	case lowerBase == lowerQuery:
		score = 1000
		matched = true
	case strings.HasPrefix(lowerBase, lowerQuery):
		score = 800
		matched = true
	case strings.Contains(lowerBase, lowerQuery):
		score = 500
		matched = true
	case strings.Contains(lowerPath, lowerQuery):
		score = 300
		matched = true
	default:
		fr := FuzzyMatch(query, relPath)
		if fr.Matches {
			score = 100.0 - fr.Score
			if score < 1 {
				score = 1
			}
			matched = true
		}
	}
	if matched && isDir {
		score += 10
	}
	return score, matched
}

func (p *CombinedAutocompleteProvider) fuzzyFileSuggestions(
	ctx context.Context,
	basePath, rawQuery string,
	quoted bool,
	maxWalk int,
	homeFn func() (string, error),
) []AutocompleteItem {
	searchRoot, displayBase, subQuery := resolveSearchScope(basePath, rawQuery, homeFn)
	if searchRoot == "" {
		return nil
	}

	type candidate struct {
		rel   string
		isDir bool
		score float64
	}
	var candidates []candidate
	count := 0

	_ = filepath.WalkDir(searchRoot, func(fullPath string, d fs.DirEntry, err error) error {
		if ctx != nil && ctx.Err() != nil {
			return filepath.SkipAll
		}
		if err != nil {
			return nil
		}
		if fullPath == searchRoot {
			return nil
		}
		name := d.Name()
		isDir := d.IsDir()
		if isDir && shouldSkipWalkDir(name) {
			return filepath.SkipDir
		}
		// Skip hidden dotfiles unless query explicitly starts with a dot segment.
		if strings.HasPrefix(name, ".") && !strings.HasPrefix(path.Base(subQuery), ".") {
			if isDir {
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(searchRoot, fullPath)
		if relErr != nil || rel == "." {
			return nil
		}
		count++
		if maxWalk > 0 && count > maxWalk {
			return filepath.SkipAll
		}
		slashRel := filepath.ToSlash(rel)
		sc, ok := scoreFileEntry(slashRel, subQuery, isDir)
		if ok {
			candidates = append(candidates, candidate{rel: slashRel, isDir: isDir, score: sc})
		}
		return nil
	})

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		di := strings.Count(candidates[i].rel, "/")
		dj := strings.Count(candidates[j].rel, "/")
		if di != dj {
			return di < dj
		}
		if len(candidates[i].rel) != len(candidates[j].rel) {
			return len(candidates[i].rel) < len(candidates[j].rel)
		}
		return candidates[i].rel < candidates[j].rel
	})

	limit := len(candidates)
	if limit > 20 {
		limit = 20
	}
	items := make([]AutocompleteItem, 0, limit)
	for _, c := range candidates[:limit] {
		displayPath := joinDisplayPath(displayBase, c.rel)
		baseName := path.Base(c.rel)
		label := baseName
		if c.isDir {
			label += "/"
		}
		items = append(items, AutocompleteItem{
			Value:       formatAtCompletionValue(displayPath, c.isDir, true, quoted),
			Label:       label,
			Description: displayPath,
		})
	}
	return items
}

func (p *CombinedAutocompleteProvider) dirPathSuggestions(
	ctx context.Context,
	basePath, rawPrefix string,
	quoted bool,
	homeFn func() (string, error),
) []AutocompleteItem {
	if ctx != nil && ctx.Err() != nil {
		return nil
	}
	cleanQuery := rawPrefix
	if quoted {
		cleanQuery = strings.TrimPrefix(cleanQuery, `"`)
	}
	searchRoot, displayBase, subQuery := resolveSearchScope(basePath, cleanQuery, homeFn)
	if searchRoot == "" {
		return nil
	}

	dirPart := path.Dir(subQuery)
	if subQuery == "" || strings.HasSuffix(subQuery, "/") {
		dirPart = strings.TrimSuffix(subQuery, "/")
	}
	nameFilter := ""
	if !strings.HasSuffix(subQuery, "/") {
		nameFilter = path.Base(subQuery)
		if subQuery == "." || subQuery == ".." {
			nameFilter = ""
		}
	}
	targetDir := searchRoot
	if dirPart != "" && dirPart != "." {
		targetDir = filepath.Join(searchRoot, filepath.FromSlash(dirPart))
	}

	entries, err := os.ReadDir(targetDir)
	if err != nil {
		return nil
	}

	type dirEntry struct {
		item  AutocompleteItem
		isDir bool
	}
	var out []dirEntry
	lowerFilter := strings.ToLower(nameFilter)
	for _, ent := range entries {
		name := ent.Name()
		if strings.HasPrefix(name, ".") && !strings.HasPrefix(nameFilter, ".") {
			continue
		}
		if lowerFilter != "" && !strings.HasPrefix(strings.ToLower(name), lowerFilter) {
			if !FuzzyMatch(lowerFilter, name).Matches {
				continue
			}
		}
		isDir := ent.IsDir()
		rel := name
		if dirPart != "" && dirPart != "." {
			rel = path.Join(dirPart, name)
		}
		disp := joinDisplayPath(displayBase, rel)
		label := name
		if isDir {
			label += "/"
		}
		out = append(out, dirEntry{
			isDir: isDir,
			item: AutocompleteItem{
				Value:       formatAtCompletionValue(disp, isDir, false, quoted),
				Label:       label,
				Description: disp,
			},
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].isDir != out[j].isDir {
			return out[i].isDir
		}
		return out[i].item.Label < out[j].item.Label
	})
	if len(out) > 20 {
		out = out[:20]
	}
	items := make([]AutocompleteItem, len(out))
	for i := range out {
		items[i] = out[i].item
	}
	return items
}

// AutocompletePopup renders the interactive completion candidate menu widget.
type AutocompletePopup struct {
	mu          sync.Mutex
	items       []AutocompleteItem
	prefix      string
	selected    int
	maxVisible  int
	theme       *GuiTheme
	onSelect    func(AutocompleteItem)
	scrollState ui.ScrollState
}

// NewAutocompletePopup constructs an autocomplete popup list widget.
func NewAutocompletePopup() *AutocompletePopup {
	return &AutocompletePopup{
		maxVisible: 8,
	}
}

// SetItems updates the items shown in the popup and clamps selection.
func (p *AutocompletePopup) SetItems(prefix string, items []AutocompleteItem) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prefix = prefix
	p.items = append([]AutocompleteItem(nil), items...)
	p.selected = 0
}

// Clear empties the popup suggestions.
func (p *AutocompletePopup) Clear() {
	p.SetItems("", nil)
}

// Items returns a copy of current completion items.
func (p *AutocompletePopup) Items() []AutocompleteItem {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]AutocompleteItem, len(p.items))
	copy(out, p.items)
	return out
}

// Prefix returns the matched token prefix for current items.
func (p *AutocompletePopup) Prefix() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.prefix
}

// Empty reports whether there are no suggestions.
func (p *AutocompletePopup) Empty() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.items) == 0
}

// SelectedIndex returns the active highlight index (-1 when empty).
func (p *AutocompletePopup) SelectedIndex() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.items) == 0 {
		return -1
	}
	return p.selected
}

// SelectedItem returns the currently highlighted completion item.
func (p *AutocompletePopup) SelectedItem() (AutocompleteItem, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.items) == 0 || p.selected < 0 || p.selected >= len(p.items) {
		return AutocompleteItem{}, false
	}
	return p.items[p.selected], true
}

// MoveUp moves selection up with wrap-around.
func (p *AutocompletePopup) MoveUp() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.items) == 0 {
		return
	}
	p.selected--
	if p.selected < 0 {
		p.selected = len(p.items) - 1
	}
}

// MoveDown moves selection down with wrap-around.
func (p *AutocompletePopup) MoveDown() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.items) == 0 {
		return
	}
	p.selected = (p.selected + 1) % len(p.items)
}

// SetMaxVisible sets the maximum visible row count before scrolling.
func (p *AutocompletePopup) SetMaxVisible(n int) {
	if n < 1 {
		n = 1
	}
	p.mu.Lock()
	p.maxVisible = n
	p.mu.Unlock()
}

// SetTheme sets an optional GuiTheme override.
func (p *AutocompletePopup) SetTheme(th *GuiTheme) {
	p.mu.Lock()
	p.theme = th
	p.mu.Unlock()
}

// SetOnSelect sets the click callback when a user clicks an autocomplete row.
func (p *AutocompletePopup) SetOnSelect(fn func(AutocompleteItem)) {
	p.mu.Lock()
	p.onSelect = fn
	p.mu.Unlock()
}

// View renders the popup list element during an immediate-mode UI build pass.
func (p *AutocompletePopup) View(c *ui.Context) ui.Element {
	p.mu.Lock()
	items := append([]AutocompleteItem(nil), p.items...)
	sel := p.selected
	maxVis := p.maxVisible
	theme := ResolveGuiTheme(c, p.theme)
	onSelect := p.onSelect
	p.mu.Unlock()

	if len(items) == 0 {
		return ui.Box(c).Hidden(true)
	}
	if maxVis <= 0 {
		maxVis = 8
	}

	rowHeight := float32(30)
	maxHeight := float32(maxVis)*rowHeight + 8

	container := ui.Column(c).
		Background(theme.SurfaceElevated).
		Border(1, theme.BorderAccent).
		Radius(theme.Radius).
		Padding(4).
		MaxHeight(maxHeight).
		MinWidth(260)

	container.Children(func() {
		scroll := ui.Scroll(c).TrackScroll(&p.scrollState).FillWidth().MaxHeight(maxHeight - 8)
		scroll.Children(func() {
			col := ui.Column(c).FillWidth().Gap(2)
			col.Children(func() {
				for idx, it := range items {
					isSelected := idx == sel
					bg := ui.Color{}
					fg := theme.Text
					descColor := theme.Muted
					if isSelected {
						bg = theme.SelectedBg
						fg = theme.SelectedText
					}

					row := ui.Row(c).
						Key(it.Value).
						FillWidth().
						Padding(6, 10).
						Radius(theme.Radius - 2).
						AlignItems(ui.Center).
						Justify(ui.SpaceBetween).
						Gap(12).
						Cursor(ui.CursorPointer)
					if bg.A > 0 {
						row.Background(bg)
					} else if row.Hovered() {
						row.Background(theme.SurfaceHover)
					}

					itemCopy := it
					itemIdx := idx
					row.OnClick(func() {
						p.mu.Lock()
						p.selected = itemIdx
						p.mu.Unlock()
						if onSelect != nil {
							onSelect(itemCopy)
						}
					})

					row.Children(func() {
						ui.Text(c, it.Label).
							TextColor(fg).
							FontWeight(500).
							SingleLine()
						if it.Description != "" && it.Description != it.Label {
							ui.Text(c, it.Description).
								TextColor(descColor).
								FontSize(12).
								SingleLine()
						}
					})
				}
			})
		})
	})

	return container
}
