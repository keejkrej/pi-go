// Ported from packages/coding-agent/src/extensions/tool-search/tool.ts (pi v1.5.2).

package toolsearchext

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
	"github.com/keejkrej/pi-go/tui"
)

const (
	// ToolSearchToolName is the registered name of the tool discovery tool.
	ToolSearchToolName = "tool_search"
	// DefaultToolSearchLimit is the default maximum number of tools returned by tool_search.
	DefaultToolSearchLimit = 8
	// ToolSearchDescription is the model-facing description for tool_search.
	ToolSearchDescription = "# Tool discovery\n\nSearches over deferred tool metadata with BM25 and exposes matching tools for the next model call.\n\nSome of the tools, such as tools of MCP servers, may not have been provided to you upfront, and you should use this tool (`" + ToolSearchToolName + "`) to search for the required tools. For MCP tool discovery, always use `" + ToolSearchToolName + "`."
)

// ToolSearchDocument is a tool as the ranker sees it: its name and the texts built by
// CreateToolSearchDocument.
type ToolSearchDocument struct {
	Name string `json:"name"`
	// Text is the description, schema descriptions, and property names.
	Text string `json:"text"`
	// NamespaceText is the name, description, and instructions of the tool's namespace.
	NamespaceText string `json:"namespaceText,omitzero"`
}

// ToolSearchMatch is one ranked tool match returned by ToolRanker.Rank.
type ToolSearchMatch struct {
	Name  string  `json:"name"`
	Score float64 `json:"score"`
}

// ToolRanker ranks tools for a query.
type ToolRanker interface {
	Rank(query string, documents []ToolSearchDocument, limit int) []ToolSearchMatch
}

var (
	toolReCamel1     = regexp.MustCompile(`([a-z0-9])([A-Z])`)
	toolReCamel2     = regexp.MustCompile(`([A-Z]+)([A-Z][a-z])`)
	toolReNonWord    = regexp.MustCompile(`[^\p{L}\p{M}\p{N}]+`)
	toolReAsciiWords = regexp.MustCompile(`^[a-z0-9 ]*$`)
	toolReAsciiTerm  = regexp.MustCompile(`^[a-z0-9]+$`)
	toolReStemEs     = regexp.MustCompile(`(ches|shes|sses|xes|zes)$`)
	toolReNormName   = regexp.MustCompile(`[^a-z0-9_$]`)
	toolReLineSplit  = regexp.MustCompile(`\r?\n`)

	toolStopWords = map[string]struct{}{
		"a": {}, "an": {}, "and": {}, "are": {}, "as": {}, "at": {},
		"be": {}, "by": {}, "for": {}, "from": {}, "in": {}, "is": {},
		"it": {}, "of": {}, "on": {}, "or": {}, "that": {}, "the": {},
		"this": {}, "to": {}, "with": {},
	}
)

// toolStem computes a naive singular form so "issues" matches "issue" and "searches" matches "search".
func toolStem(term string) string {
	if len(term) > 4 && strings.HasSuffix(term, "ies") {
		return term[:len(term)-3] + "y"
	}
	if len(term) > 4 && toolReStemEs.MatchString(term) {
		return term[:len(term)-2]
	}
	if len(term) > 3 && strings.HasSuffix(term, "s") && !strings.HasSuffix(term, "ss") {
		return term[:len(term)-1]
	}
	return term
}

// Tokenize applies Unicode word segmentation with identifier boundaries and English stop words/singular forms.
func Tokenize(text string) []string {
	normalized := js.Normalize(text, "NFC")
	normalized = toolReCamel1.ReplaceAllString(normalized, "$1 $2")
	normalized = toolReCamel2.ReplaceAllString(normalized, "$1 $2")
	normalized = js.ToLower(normalized)
	normalized = toolReNonWord.ReplaceAllString(normalized, " ")

	var words []string
	if toolReAsciiWords.MatchString(normalized) {
		words = strings.Split(normalized, " ")
	} else {
		segs := tui.GetWordSegmenter().Segment(normalized)
		words = make([]string, 0, len(segs))
		for _, seg := range segs {
			if seg.IsWordLike != nil && *seg.IsWordLike {
				words = append(words, seg.Segment)
			}
		}
	}

	out := make([]string, 0, len(words))
	for _, word := range words {
		if word == "" {
			continue
		}
		if _, stop := toolStopWords[word]; stop {
			continue
		}
		if toolReAsciiTerm.MatchString(word) {
			word = toolStem(word)
		}
		out = append(out, word)
	}
	return out
}

func toolSchemaObject(v any) (*jsonx.Object, bool) {
	switch s := v.(type) {
	case *typebox.Schema:
		if s == nil || s.Object == nil {
			return nil, false
		}
		return s.Object, true
	case *jsonx.Object:
		if s == nil {
			return nil, false
		}
		return s, true
	default:
		return nil, false
	}
}

// ToolResolveSchemaRef resolves a local JSON Pointer schema reference ("#" or "#/...").
func ToolResolveSchemaRef(ref string, root any) any {
	if ref != "#" && !strings.HasPrefix(ref, "#/") {
		return nil
	}
	var current any = root
	if obj, ok := toolSchemaObject(root); ok {
		current = obj
	}
	rest := ""
	if len(ref) > 2 {
		rest = ref[2:]
	}
	for _, segment := range strings.Split(rest, "/") {
		if segment == "" {
			continue
		}
		decoded, err := js.DecodeURIComponent(segment)
		if err != nil {
			return nil
		}
		key := strings.ReplaceAll(strings.ReplaceAll(decoded, "~1", "/"), "~0", "~")
		switch cur := current.(type) {
		case *typebox.Schema:
			if cur == nil || cur.Object == nil {
				return nil
			}
			next, exists := cur.Object.Get(key)
			if !exists {
				return nil
			}
			current = next
		case *jsonx.Object:
			if cur == nil {
				return nil
			}
			next, exists := cur.Get(key)
			if !exists {
				return nil
			}
			current = next
		case map[string]any:
			next, exists := cur[key]
			if !exists {
				return nil
			}
			current = next
		case []any:
			idx, err := strconv.Atoi(key)
			if err != nil || idx < 0 || idx >= len(cur) {
				return nil
			}
			current = cur[idx]
		default:
			return nil
		}
	}
	if _, ok := current.(bool); ok {
		return current
	}
	if obj, ok := toolSchemaObject(current); ok {
		return obj
	}
	if m, ok := current.(map[string]any); ok {
		return m
	}
	return nil
}

func toolSchemaGet(v any, key string) (any, bool) {
	switch cur := v.(type) {
	case *typebox.Schema:
		if cur == nil || cur.Object == nil {
			return nil, false
		}
		return cur.Object.Get(key)
	case *jsonx.Object:
		if cur == nil {
			return nil, false
		}
		return cur.Get(key)
	case map[string]any:
		val, ok := cur[key]
		return val, ok
	default:
		return nil, false
	}
}

// toolSchemaText collects schema descriptions and property names recursively, following local $refs into root.
func toolSchemaText(schema any, root any, parts *[]string, seen map[*jsonx.Object]struct{}) {
	obj, ok := toolSchemaObject(schema)
	if !ok {
		return
	}
	if refVal, hasRef := obj.Get("$ref"); hasRef {
		if refStr, ok := refVal.(string); ok {
			target := ToolResolveSchemaRef(refStr, root)
			if targetObj, ok := toolSchemaObject(target); ok {
				if _, visited := seen[targetObj]; !visited {
					seen[targetObj] = struct{}{}
					toolSchemaText(targetObj, root, parts, seen)
				}
			}
		}
	}
	if descVal, hasDesc := obj.Get("description"); hasDesc {
		if desc, ok := descVal.(string); ok {
			*parts = append(*parts, desc)
		}
	}
	if propsVal, hasProps := obj.Get("properties"); hasProps {
		if propsObj, ok := toolSchemaObject(propsVal); ok {
			for _, name := range propsObj.Keys() {
				prop, _ := propsObj.Get(name)
				*parts = append(*parts, name)
				toolSchemaText(prop, root, parts, seen)
			}
		}
	}
	if itemsVal, hasItems := toolSchemaGet(obj, "items"); hasItems {
		toolSchemaText(itemsVal, root, parts, seen)
	}
	for _, key := range []string{"anyOf", "oneOf", "allOf"} {
		if variantsVal, hasKey := obj.Get(key); hasKey {
			if variants, ok := variantsVal.([]any); ok {
				for _, variant := range variants {
					toolSchemaText(variant, root, parts, seen)
				}
			}
		}
	}
}

// CreateToolSearchDocument builds the searchable document for a tool and optional namespace.
func CreateToolSearchDocument(tool core.ToolInfo, namespace ...*core.ToolNamespace) ToolSearchDocument {
	parts := []string{tool.Description}
	seen := make(map[*jsonx.Object]struct{})
	if rootObj, ok := toolSchemaObject(tool.Parameters); ok {
		seen[rootObj] = struct{}{}
	}
	toolSchemaText(tool.Parameters, tool.Parameters, &parts, seen)

	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if js.Trim(part) != "" {
			filtered = append(filtered, part)
		}
	}

	var ns *core.ToolNamespace
	if len(namespace) > 0 {
		ns = namespace[0]
	}
	namespaceText := ""
	if ns != nil {
		desc := ""
		if ns.Description != nil {
			desc = *ns.Description
		}
		inst := ""
		if ns.Instructions != nil {
			inst = *ns.Instructions
		}
		namespaceText = strings.Join([]string{ns.Name, desc, inst}, " ")
	}
	return ToolSearchDocument{
		Name:          tool.Name,
		Text:          strings.Join(filtered, " "),
		NamespaceText: namespaceText,
	}
}

func toolNormalizeName(name string) string {
	return toolReNormName.ReplaceAllString(js.ToLower(js.Trim(name)), "_")
}

func toolIsExactNameQuery(query, name string) bool {
	normalized := toolNormalizeName(name)
	return normalized == query || strings.HasSuffix(normalized, "__"+query)
}

type toolIndexedDocument struct {
	name           string
	nameTerms      map[string]struct{}
	localNameTerms []string
	textCounts     map[string]int
	textLength     int
	namespaceTerms map[string]struct{}
}

func toolTermSet(terms []string) map[string]struct{} {
	set := make(map[string]struct{}, len(terms))
	for _, term := range terms {
		set[term] = struct{}{}
	}
	return set
}

func toolUniqueTerms(terms []string) []string {
	seen := make(map[string]struct{}, len(terms))
	out := make([]string, 0, len(terms))
	for _, term := range terms {
		if _, ok := seen[term]; ok {
			continue
		}
		seen[term] = struct{}{}
		out = append(out, term)
	}
	return out
}

func toolIndexDocument(document ToolSearchDocument) toolIndexedDocument {
	textCounts := make(map[string]int)
	textLength := 0
	for _, term := range Tokenize(document.Text) {
		textCounts[term]++
		textLength++
	}
	segments := strings.Split(document.Name, "__")
	return toolIndexedDocument{
		name:           document.Name,
		nameTerms:      toolTermSet(Tokenize(document.Name)),
		localNameTerms: toolUniqueTerms(Tokenize(segments[len(segments)-1])),
		textCounts:     textCounts,
		textLength:     textLength,
		namespaceTerms: toolTermSet(Tokenize(document.NamespaceText)),
	}
}

const (
	toolNameWeight      = 3.0
	toolNamespaceWeight = 0.5
)

// Bm25RankerOptions configures a Bm25Ranker.
type Bm25RankerOptions struct {
	K1 *float64
	B  *float64
}

// Bm25Ranker implements Okapi BM25 ranking over tool metadata.
type Bm25Ranker struct {
	k1 float64
	b  float64
}

// NewBm25Ranker creates a Bm25Ranker with default k1=1.2, b=0.75 unless overridden.
func NewBm25Ranker(options ...*Bm25RankerOptions) *Bm25Ranker {
	k1 := 1.2
	b := 0.75
	if len(options) > 0 && options[0] != nil {
		if options[0].K1 != nil {
			k1 = *options[0].K1
		}
		if options[0].B != nil {
			b = *options[0].B
		}
	}
	return &Bm25Ranker{k1: k1, b: b}
}

type toolScoredMatch struct {
	name    string
	score   float64
	covered int
	exact   bool
}

// Rank scores documents against query and returns at most limit matches.
func (r *Bm25Ranker) Rank(query string, documents []ToolSearchDocument, limit int) []ToolSearchMatch {
	queryTerms := toolUniqueTerms(Tokenize(query))
	if len(queryTerms) == 0 || len(documents) == 0 || limit <= 0 {
		return []ToolSearchMatch{}
	}
	queryTermSet := toolTermSet(queryTerms)

	indexed := make([]toolIndexedDocument, len(documents))
	totalLength := 0
	for i, doc := range documents {
		indexed[i] = toolIndexDocument(doc)
		totalLength += indexed[i].textLength
	}
	averageLength := float64(totalLength) / float64(len(indexed))
	if averageLength == 0 {
		averageLength = 1
	}

	idf := make(map[string]float64, len(queryTerms))
	fullNameBonus := 0.0
	nDocs := float64(len(indexed))
	for _, term := range queryTerms {
		frequency := 0
		for _, doc := range indexed {
			_, inName := doc.nameTerms[term]
			_, inText := doc.textCounts[term]
			_, inNs := doc.namespaceTerms[term]
			if inName || inText || inNs {
				frequency++
			}
		}
		f := float64(frequency)
		val := math.Log(1 + (nDocs-f+0.5)/(f+0.5))
		idf[term] = val
		fullNameBonus += val
	}

	exactQuery := toolNormalizeName(query)
	matches := make([]toolScoredMatch, 0)
	for _, doc := range indexed {
		norm := r.k1 * (1 - r.b + (r.b*float64(doc.textLength))/averageLength)
		score := 0.0
		covered := 0
		for _, term := range queryTerms {
			count := float64(doc.textCounts[term])
			weight := (count * (r.k1 + 1)) / (count + norm)
			if _, ok := doc.nameTerms[term]; ok {
				weight += toolNameWeight
			}
			if _, ok := doc.namespaceTerms[term]; ok {
				weight += toolNamespaceWeight
			}
			if weight > 0 {
				covered++
			}
			score += idf[term] * weight
		}
		if score == 0 {
			continue
		}
		if len(doc.localNameTerms) > 0 {
			allLocal := true
			for _, term := range doc.localNameTerms {
				if _, ok := queryTermSet[term]; !ok {
					allLocal = false
					break
				}
			}
			if allLocal {
				score += fullNameBonus
			}
		}
		matches = append(matches, toolScoredMatch{
			name:    doc.name,
			score:   score,
			covered: covered,
			exact:   toolIsExactNameQuery(exactQuery, doc.name),
		})
	}

	slices.SortStableFunc(matches, func(a, b toolScoredMatch) int {
		if a.exact != b.exact {
			if b.exact {
				return 1
			}
			return -1
		}
		if a.covered != b.covered {
			return cmp.Compare(b.covered, a.covered)
		}
		return cmp.Compare(b.score, a.score)
	})

	if len(matches) > limit {
		matches = matches[:limit]
	}
	out := make([]ToolSearchMatch, len(matches))
	for i, m := range matches {
		out[i] = ToolSearchMatch{Name: m.name, Score: m.score}
	}
	return out
}

func toolBuildSearchSchema() *typebox.Schema {
	props := omap.NewMap[string, *typebox.Schema]()
	props.Set("query", typebox.String(jsonx.ObjectOf("description", "Search query for deferred tools.")))
	props.Set("limit", typebox.Optional(typebox.Number(jsonx.ObjectOf(
		"description", fmt.Sprintf("Maximum number of tools to return. Defaults to %d.", DefaultToolSearchLimit),
	))))
	return typebox.Object(props)
}

// ToolSearchSchema is the parameter schema for the tool_search tool.
var ToolSearchSchema = toolBuildSearchSchema()

// ToolSearchInput is the input shape of tool_search.
type ToolSearchInput struct {
	Query string `json:"query"`
	Limit *int   `json:"limit,omitzero"`
}

// ToolSearchResultTool describes a loaded tool returned by searchAndLoad.
type ToolSearchResultTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ToolSearchToolDetails holds the tool names loaded by a tool_search invocation.
type ToolSearchToolDetails struct {
	Loaded []string `json:"loaded"`
}

// ToolSearchToolsAPI is the subset of core.ExtensionAPI needed by tool_search.
type ToolSearchToolsAPI interface {
	GetAllTools() []core.ToolInfo
	GetActiveTools() []string
	SetActiveTools(toolNames []string)
}

// ToolSearchToolOptions configures CreateToolSearchToolDefinition.
type ToolSearchToolOptions struct {
	Tools ToolSearchToolsAPI
}

func toolIsSearchable(exposure core.ToolExposure) bool {
	return exposure == core.ToolExposureCodemode || exposure == core.ToolExposureDeferred
}

func toolSearchAndLoad(tools ToolSearchToolsAPI, query string, limit int) []ToolSearchResultTool {
	active := tools.GetActiveTools()
	activeSet := make(map[string]struct{}, len(active))
	for _, name := range active {
		activeSet[name] = struct{}{}
	}

	allTools := tools.GetAllTools()
	candidates := make([]core.ToolInfo, 0, len(allTools))
	byName := make(map[string]string)
	for _, tool := range allTools {
		if !toolIsSearchable(tool.Exposure) {
			continue
		}
		if _, isActive := activeSet[tool.Name]; isActive {
			continue
		}
		candidates = append(candidates, tool)
		byName[tool.Name] = tool.Description
	}

	documents := make([]ToolSearchDocument, len(candidates))
	for i, candidate := range candidates {
		documents[i] = CreateToolSearchDocument(candidate, candidate.Namespace)
	}
	matches := NewBm25Ranker().Rank(query, documents, limit)
	if len(matches) > 0 {
		nextActive := make([]string, 0, len(active)+len(matches))
		nextActive = append(nextActive, active...)
		for _, match := range matches {
			nextActive = append(nextActive, match.Name)
		}
		tools.SetActiveTools(nextActive)
	}

	out := make([]ToolSearchResultTool, len(matches))
	for i, match := range matches {
		out[i] = ToolSearchResultTool{
			Name:        match.Name,
			Description: byName[match.Name],
		}
	}
	return out
}

func toolParseSearchParams(params *jsonx.Object) (string, int, error) {
	if params == nil {
		return "", 0, errors.New("query must not be empty")
	}
	query, hasQuery := params.GetString("query")
	if !hasQuery || js.Trim(query) == "" {
		return "", 0, errors.New("query must not be empty")
	}
	maxLimit := DefaultToolSearchLimit
	if rawLimit, hasLimit := params.Get("limit"); hasLimit && rawLimit != nil {
		num, ok := rawLimit.(float64)
		if !ok {
			if i, okInt := rawLimit.(int); okInt {
				num = float64(i)
				ok = true
			}
		}
		if !ok || math.IsNaN(num) || math.IsInf(num, 0) || math.Trunc(num) != num || num <= 0 {
			return "", 0, errors.New("limit must be a positive integer")
		}
		maxLimit = int(num)
	}
	return query, maxLimit, nil
}

// CreateToolSearchToolDefinition builds the core.ToolDefinition for tool_search.
func CreateToolSearchToolDefinition(options ...*ToolSearchToolOptions) core.ToolDefinition {
	var opts ToolSearchToolOptions
	if len(options) > 0 && options[0] != nil {
		opts = *options[0]
	}
	snippet := "Search for tools that are not loaded yet and load the matches"
	return core.ToolDefinition{
		Name:          ToolSearchToolName,
		Label:         ToolSearchToolName,
		Description:   ToolSearchDescription,
		PromptSnippet: &snippet,
		Parameters:    ToolSearchSchema,
		Exposure:      core.ToolExposureModelOnly,
		ReachesTools: func(*jsonx.Object) core.ToolReachesResult {
			return core.ToolReachesResult{All: true}
		},
		Execute: func(_ context.Context, _ string, params *jsonx.Object, _ agent.AgentToolUpdateCallback, _ core.ExtensionToolContext) (agent.AgentToolResult, error) {
			query, maxLimit, err := toolParseSearchParams(params)
			if err != nil {
				return agent.AgentToolResult{}, err
			}
			var loadedTools []ToolSearchResultTool
			if opts.Tools != nil {
				loadedTools = toolSearchAndLoad(opts.Tools, query, maxLimit)
			}
			loadedNames := make([]string, len(loadedTools))
			for i, t := range loadedTools {
				loadedNames[i] = t.Name
			}
			var text string
			if len(loadedTools) == 0 {
				text = "No matching tools found."
			} else {
				plural := "s"
				if len(loadedTools) == 1 {
					plural = ""
				}
				lines := make([]string, len(loadedTools))
				for i, t := range loadedTools {
					firstLine := toolReLineSplit.Split(js.Trim(t.Description), 2)[0]
					lines[i] = fmt.Sprintf("- %s: %s", t.Name, firstLine)
				}
				text = fmt.Sprintf("Loaded %d tool%s. They are available from your next call:\n%s", len(loadedTools), plural, strings.Join(lines, "\n"))
			}
			return agent.AgentToolResult{
				Content: ai.ContentList{&ai.TextContent{Type: "text", Text: text}},
				Details: ToolSearchToolDetails{Loaded: loadedNames},
			}, nil
		},
	}
}
