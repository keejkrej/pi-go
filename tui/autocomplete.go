// Ported from packages/tui/src/autocomplete.ts (pi v1.0.0).

package tui

import (
	"context"
	"sync"

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
	panic("unported: NewCombinedAutocompleteProvider")
}

// TriggerCharacters returns the natural trigger characters. Nil means unset.
func (p *CombinedAutocompleteProvider) TriggerCharacters() []string { return p.triggerCharacters }

// SetTriggerCharacters sets the natural trigger characters. Nil clears them.
func (p *CombinedAutocompleteProvider) SetTriggerCharacters(chars []string) {
	p.triggerCharacters = chars
}

// GetSuggestions returns slash-command, attachment, or path suggestions, or nil when none match.
func (p *CombinedAutocompleteProvider) GetSuggestions(ctx context.Context, lines []string, cursorLine int, cursorCol int, options *AutocompleteSuggestionsOptions) (*AutocompleteSuggestions, error) {
	panic("unported: CombinedAutocompleteProvider.GetSuggestions")
}

// ApplyCompletion replaces the active prefix with item and returns the new cursor.
func (p *CombinedAutocompleteProvider) ApplyCompletion(lines []string, cursorLine int, cursorCol int, item AutocompleteItem, prefix string) AutocompleteApplyResult {
	panic("unported: CombinedAutocompleteProvider.ApplyCompletion")
}

// ShouldTriggerFileCompletion reports whether Tab should open file completion.
func (p *CombinedAutocompleteProvider) ShouldTriggerFileCompletion(lines []string, cursorLine int, cursorCol int) bool {
	panic("unported: CombinedAutocompleteProvider.ShouldTriggerFileCompletion")
}

func (p *CombinedAutocompleteProvider) extractAtPrefix(text string) *string {
	panic("unported: CombinedAutocompleteProvider.extractAtPrefix")
}

func (p *CombinedAutocompleteProvider) extractPathPrefix(text string, forceExtract bool) *string {
	panic("unported: CombinedAutocompleteProvider.extractPathPrefix")
}

func (p *CombinedAutocompleteProvider) expandHomePath(path string) string {
	panic("unported: CombinedAutocompleteProvider.expandHomePath")
}

func (p *CombinedAutocompleteProvider) resolveScopedFuzzyQuery(rawQuery string) *autoScopedFuzzyQuery {
	panic("unported: CombinedAutocompleteProvider.resolveScopedFuzzyQuery")
}

func (p *CombinedAutocompleteProvider) scopedPathForDisplay(displayBase string, relativePath string) string {
	panic("unported: CombinedAutocompleteProvider.scopedPathForDisplay")
}

func (p *CombinedAutocompleteProvider) getFileSuggestions(prefix string) []AutocompleteItem {
	panic("unported: CombinedAutocompleteProvider.getFileSuggestions")
}

func (p *CombinedAutocompleteProvider) scoreEntry(filePath string, query string, isDirectory bool) int {
	panic("unported: CombinedAutocompleteProvider.scoreEntry")
}

func (p *CombinedAutocompleteProvider) getBaseDirSuggestions(ctx context.Context, baseDir string, query string) ([]autoPathEntry, error) {
	panic("unported: CombinedAutocompleteProvider.getBaseDirSuggestions")
}

func (p *CombinedAutocompleteProvider) getFuzzyFileSuggestions(ctx context.Context, query string, options autoFuzzyFileOptions) ([]AutocompleteItem, error) {
	panic("unported: CombinedAutocompleteProvider.getFuzzyFileSuggestions")
}

func autoToDisplayPath(value string) string {
	panic("unported: autoToDisplayPath")
}

func autoEscapeRegex(value string) string {
	panic("unported: autoEscapeRegex")
}

func autoBuildFdPathQuery(query string) string {
	panic("unported: autoBuildFdPathQuery")
}

func autoFindLastDelimiter(text string) int {
	panic("unported: autoFindLastDelimiter")
}

func autoStripLeadingWrappers(token string) string {
	panic("unported: autoStripLeadingWrappers")
}

func autoFindUnclosedQuoteStart(text string) *int {
	panic("unported: autoFindUnclosedQuoteStart")
}

func autoIsTokenStart(text string, index int) bool {
	panic("unported: autoIsTokenStart")
}

func autoExtractQuotedPrefix(text string) *string {
	panic("unported: autoExtractQuotedPrefix")
}

func autoParsePathPrefix(prefix string) autoParsedPrefix {
	panic("unported: autoParsePathPrefix")
}

func autoBuildCompletionValue(path string, options autoCompletionValueOptions) string {
	panic("unported: autoBuildCompletionValue")
}

func autoWalkDirectoryWithFd(ctx context.Context, baseDir string, fdPath string, query string, maxResults int, maxDepth *int) ([]autoPathEntry, error) {
	panic("unported: autoWalkDirectoryWithFd")
}

var _ AutocompleteProvider = (*CombinedAutocompleteProvider)(nil)
