// Ported from packages/tui/src/alt-screen-search.ts (pi v1.0.0).

package tui

import "regexp"

// assPrintableAscii matches a line of printable ASCII. Those lines are indexed by non-space runs.
var assPrintableAscii = regexp.MustCompile(`^[\x20-\x7e]*$`)

type assSearchSourceSpan struct {
	textStart     int
	textEnd       int
	row           int
	startCol      int
	endCol        int
	linearColumns bool
}

type assSearchCorpus struct {
	text  string
	spans []assSearchSourceSpan
}

// AltScreenSearchSegment is one visible column range of a match on a content row.
type AltScreenSearchSegment struct {
	Row      int `json:"row"`
	StartCol int `json:"startCol"`
	EndCol   int `json:"endCol"`
}

// AltScreenSearchMatch is one query hit. Segments are in source order.
type AltScreenSearchMatch struct {
	Segments []AltScreenSearchSegment `json:"segments"`
}

// AltScreenSearchResult is one search against a corpus.
// Changed is false when the same lines and normalized query were searched before.
type AltScreenSearchResult struct {
	Matches []AltScreenSearchMatch `json:"matches"`
	Changed bool                   `json:"changed"`
}

func assBuildSearchCorpus(lines []string) *assSearchCorpus {
	panic("unported: assBuildSearchCorpus")
}

func assNormalizeQuery(query string) string {
	panic("unported: assNormalizeQuery")
}

func assEscapeRegExp(text string) string {
	panic("unported: assEscapeRegExp")
}

func assFindSearchCorpusMatches(corpus *assSearchCorpus, normalizedQuery string) []AltScreenSearchMatch {
	panic("unported: assFindSearchCorpusMatches")
}

// AltScreenSearchIndex caches the searchable corpus and matches while rendered transcript lines remain unchanged.
type AltScreenSearchIndex struct {
	sourceLines     []string
	corpus          *assSearchCorpus
	normalizedQuery *string
	matches         []AltScreenSearchMatch
}

// NewAltScreenSearchIndex returns an empty index.
func NewAltScreenSearchIndex() *AltScreenSearchIndex {
	return &AltScreenSearchIndex{matches: []AltScreenSearchMatch{}}
}

// Search returns the matches for query and whether the corpus or the normalized query changed.
func (i *AltScreenSearchIndex) Search(lines []string, query string) AltScreenSearchResult {
	panic("unported: AltScreenSearchIndex.Search")
}

// FindAltScreenSearchMatches returns the matches for query in lines.
func FindAltScreenSearchMatches(lines []string, query string) []AltScreenSearchMatch {
	panic("unported: FindAltScreenSearchMatches")
}

// GetAltScreenSearchMatchKey identifies a match by its first and last segment.
// It is empty when the match has no segments.
func GetAltScreenSearchMatchKey(match AltScreenSearchMatch) string {
	panic("unported: GetAltScreenSearchMatchKey")
}

// AltScreenSearchComponent is the transcript find overlay.
type AltScreenSearchComponent struct {
	input                 *Input
	onQueryChange         func(query string)
	navigationButtonStyle func(text string, hovered bool) string
	resultCount           int
	resultIndex           int // TS initial value -1
	// Button column ranges are -1 until render (TS initial value).
	previousButtonStart        int
	previousButtonEnd          int
	nextButtonStart            int
	nextButtonEnd              int
	hoveredNavigationDirection *int // -1, 1, or nil
	focused                    bool
}

// NewAltScreenSearchComponent builds the find overlay.
// navigationButtonStyle nil leaves the button text unchanged.
func NewAltScreenSearchComponent(onQueryChange func(query string), navigationButtonStyle func(text string, hovered bool) string) *AltScreenSearchComponent {
	panic("unported: NewAltScreenSearchComponent")
}

// Focused reports whether the find overlay has keyboard focus.
func (c *AltScreenSearchComponent) Focused() bool { return c.focused }

// SetFocused sets keyboard focus on the overlay and its input.
func (c *AltScreenSearchComponent) SetFocused(value bool) {
	panic("unported: AltScreenSearchComponent.SetFocused")
}

// SetResult records the selected match index and the match count.
// index -1 means there is no selected match.
func (c *AltScreenSearchComponent) SetResult(index, count int) {
	panic("unported: AltScreenSearchComponent.SetResult")
}

// GetNavigationDirectionAt returns -1 or 1 when column on row is a navigation button, otherwise nil.
func (c *AltScreenSearchComponent) GetNavigationDirectionAt(row, column int) *int {
	panic("unported: AltScreenSearchComponent.GetNavigationDirectionAt")
}

// SetHoveredNavigationDirection sets the hovered button. direction is -1, 1, or nil.
// It reports whether the hovered button changed.
func (c *AltScreenSearchComponent) SetHoveredNavigationDirection(direction *int) bool {
	panic("unported: AltScreenSearchComponent.SetHoveredNavigationDirection")
}

// HandleInput forwards a key to the query input and reports query changes.
func (c *AltScreenSearchComponent) HandleInput(data string) {
	panic("unported: AltScreenSearchComponent.HandleInput")
}

// Invalidate drops the input's cached render state.
func (c *AltScreenSearchComponent) Invalidate() {
	panic("unported: AltScreenSearchComponent.Invalidate")
}

// Render draws the find box for width columns.
func (c *AltScreenSearchComponent) Render(width int) []string {
	panic("unported: AltScreenSearchComponent.Render")
}
