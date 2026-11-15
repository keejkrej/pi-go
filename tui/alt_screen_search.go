// Ported from packages/tui/src/alt-screen-search.ts (pi v1.0.0).

package tui

import (
	"fmt"
	"regexp"
	"runtime"
	"slices"
	"strings"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsre"
)

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
	segmenter := GetGraphemeSegmenter()
	var chunks strings.Builder
	var spans []assSearchSourceSpan
	textLength := 0
	pendingSeparator := false

	appendSeparator := func() {
		if !pendingSeparator {
			return
		}
		chunks.WriteByte(' ')
		textLength++
		pendingSeparator = false
	}

	for row, rawLine := range lines {
		line := StripTerminalSequences(rawLine)
		column := 0

		if assPrintableAscii.MatchString(line) {
			index := 0
			n := len(line)
			for index < n {
				if line[index] == ' ' {
					if textLength > 0 {
						pendingSeparator = true
					}
					column++
					index++
					continue
				}
				end := index + 1
				for end < n && line[end] != ' ' {
					end++
				}
				appendSeparator()
				text := line[index:end]
				chunks.WriteString(text)
				spans = append(spans, assSearchSourceSpan{
					textStart:     textLength,
					textEnd:       textLength + len(text),
					row:           row,
					startCol:      column,
					endCol:        column + len(text),
					linearColumns: true,
				})
				textLength += len(text)
				column += len(text)
				index = end
			}
		} else {
			for _, grapheme := range segmenter.Segment(line) {
				text := grapheme.Segment
				width := VisibleWidth(text)
				isSpace := text != ""
				for _, r := range text {
					if !js.IsJSWhitespace(r) {
						isSpace = false
						break
					}
				}
				if isSpace {
					if textLength > 0 {
						pendingSeparator = true
					}
					column += width
					continue
				}
				appendSeparator()
				chunks.WriteString(text)
				u16Len := js.Len(text)
				spans = append(spans, assSearchSourceSpan{
					textStart:     textLength,
					textEnd:       textLength + u16Len,
					row:           row,
					startCol:      column,
					endCol:        column + width,
					linearColumns: false,
				})
				textLength += u16Len
				column += width
			}
		}
		if textLength > 0 {
			pendingSeparator = true
		}
	}

	return &assSearchCorpus{
		text:  chunks.String(),
		spans: spans,
	}
}

var assQueryWhitespaceRegex = jsre.MustCompile(`\s+`, "gu")
var assEscapeRegExpReplacer = strings.NewReplacer(
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

func assNormalizeQuery(query string) string {
	return js.Trim(assQueryWhitespaceRegex.Replace(query, " "))
}

func assEscapeRegExp(text string) string {
	return assEscapeRegExpReplacer.Replace(text)
}

func assFindSearchCorpusMatches(corpus *assSearchCorpus, normalizedQuery string) []AltScreenSearchMatch {
	if normalizedQuery == "" || corpus == nil {
		return nil
	}
	expression := jsre.MustCompile(assEscapeRegExp(normalizedQuery), "giu")
	var matches []AltScreenSearchMatch
	spanIndex := 0

	for _, match := range expression.MatchAll(corpus.text) {
		start := match.Index
		end := match.End()
		for spanIndex < len(corpus.spans) && corpus.spans[spanIndex].textEnd <= start {
			spanIndex++
		}

		var segments []AltScreenSearchSegment
		for index := spanIndex; index < len(corpus.spans); index++ {
			span := corpus.spans[index]
			if span.textStart >= end {
				break
			}
			if span.textEnd <= start {
				continue
			}
			startCol := span.startCol
			endCol := span.endCol
			if span.linearColumns {
				overlapStart := start
				if span.textStart > overlapStart {
					overlapStart = span.textStart
				}
				overlapEnd := end
				if span.textEnd < overlapEnd {
					overlapEnd = span.textEnd
				}
				startCol = span.startCol + overlapStart - span.textStart
				endCol = span.startCol + overlapEnd - span.textStart
			}
			if n := len(segments); n > 0 && segments[n-1].Row == span.row && startCol <= segments[n-1].EndCol {
				if endCol > segments[n-1].EndCol {
					segments[n-1].EndCol = endCol
				}
			} else {
				segments = append(segments, AltScreenSearchSegment{
					Row:      span.row,
					StartCol: startCol,
					EndCol:   endCol,
				})
			}
		}
		for spanIndex < len(corpus.spans) && corpus.spans[spanIndex].textEnd <= end {
			spanIndex++
		}
		if len(segments) > 0 {
			matches = append(matches, AltScreenSearchMatch{Segments: segments})
		}
	}

	return matches
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
	sourceChanged := i.sourceLines == nil || len(i.sourceLines) != len(lines)
	if !sourceChanged {
		for idx, line := range lines {
			if i.sourceLines[idx] != line {
				sourceChanged = true
				break
			}
		}
	}
	if sourceChanged || i.corpus == nil {
		i.sourceLines = slices.Clone(lines)
		i.corpus = assBuildSearchCorpus(lines)
	}

	normalizedQuery := assNormalizeQuery(query)
	changed := sourceChanged || i.normalizedQuery == nil || *i.normalizedQuery != normalizedQuery
	if changed {
		nq := normalizedQuery
		i.normalizedQuery = &nq
		i.matches = assFindSearchCorpusMatches(i.corpus, normalizedQuery)
	}
	return AltScreenSearchResult{
		Matches: i.matches,
		Changed: changed,
	}
}

// FindAltScreenSearchMatches returns the matches for query in lines.
func FindAltScreenSearchMatches(lines []string, query string) []AltScreenSearchMatch {
	normalizedQuery := assNormalizeQuery(query)
	if normalizedQuery == "" {
		return nil
	}
	return assFindSearchCorpusMatches(assBuildSearchCorpus(lines), normalizedQuery)
}

// GetAltScreenSearchMatchKey identifies a match by its first and last segment.
// It is empty when the match has no segments.
func GetAltScreenSearchMatchKey(match AltScreenSearchMatch) string {
	if len(match.Segments) == 0 {
		return ""
	}
	first := match.Segments[0]
	last := match.Segments[len(match.Segments)-1]
	return fmt.Sprintf("%d:%d:%d:%d", first.Row, first.StartCol, last.Row, last.EndCol)
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
	prompt := " "
	placeholder := "Find in transcript"
	input := NewInput(&InputOptions{
		Prompt:      &prompt,
		Placeholder: &placeholder,
		PlaceholderStyle: func(text string) string {
			return "\x1b[2m" + text + "\x1b[22m"
		},
	})
	if navigationButtonStyle == nil {
		navigationButtonStyle = func(text string, _ bool) string { return text }
	}
	return &AltScreenSearchComponent{
		input:                 input,
		onQueryChange:         onQueryChange,
		navigationButtonStyle: navigationButtonStyle,
		resultCount:           0,
		resultIndex:           -1,
		previousButtonStart:   -1,
		previousButtonEnd:     -1,
		nextButtonStart:       -1,
		nextButtonEnd:         -1,
	}
}

// Focused reports whether the find overlay has keyboard focus.
func (c *AltScreenSearchComponent) Focused() bool { return c.focused }

// SetFocused sets keyboard focus on the overlay and its input.
func (c *AltScreenSearchComponent) SetFocused(value bool) {
	c.focused = value
	c.input.SetFocused(value)
}

// WantsKeyRelease reports whether key-release events should be delivered.
func (c *AltScreenSearchComponent) WantsKeyRelease() bool { return false }

// SetResult records the selected match index and the match count.
// index -1 means there is no selected match.
func (c *AltScreenSearchComponent) SetResult(index, count int) {
	c.resultIndex = index
	c.resultCount = count
}

// GetNavigationDirectionAt returns -1 or 1 when column on row is a navigation button, otherwise nil.
func (c *AltScreenSearchComponent) GetNavigationDirectionAt(row, column int) *int {
	if row != 2 {
		return nil
	}
	if column >= c.previousButtonStart && column < c.previousButtonEnd {
		dir := -1
		return &dir
	}
	if column >= c.nextButtonStart && column < c.nextButtonEnd {
		dir := 1
		return &dir
	}
	return nil
}

// SetHoveredNavigationDirection sets the hovered button. direction is -1, 1, or nil.
// It reports whether the hovered button changed.
func (c *AltScreenSearchComponent) SetHoveredNavigationDirection(direction *int) bool {
	if direction == nil && c.hoveredNavigationDirection == nil {
		return false
	}
	if direction != nil && c.hoveredNavigationDirection != nil && *direction == *c.hoveredNavigationDirection {
		return false
	}
	if direction == nil {
		c.hoveredNavigationDirection = nil
	} else {
		dir := *direction
		c.hoveredNavigationDirection = &dir
	}
	return true
}

// HandleInput forwards a key to the query input and reports query changes.
func (c *AltScreenSearchComponent) HandleInput(data string) {
	previous := c.input.GetValue()
	c.input.HandleInput(data)
	query := c.input.GetValue()
	if query != previous && c.onQueryChange != nil {
		c.onQueryChange(query)
	}
}

// Invalidate drops the input's cached render state.
func (c *AltScreenSearchComponent) Invalidate() {
	c.input.Invalidate()
}

// Render draws the find box for width columns.
func (c *AltScreenSearchComponent) Render(width int) []string {
	safeWidth := width
	if safeWidth < 1 {
		safeWidth = 1
	}
	innerWidth := safeWidth - 2
	if innerWidth < 0 {
		innerWidth = 0
	}

	formatKey := func(keys []KeyId) string {
		if len(keys) == 0 || keys[0] == "" {
			return "Unbound"
		}
		parts := strings.Split(string(keys[0]), "+")
		for idx, part := range parts {
			if runtime.GOOS == "darwin" && strings.ToLower(part) == "alt" {
				parts[idx] = "Option"
			} else if len(part) > 0 {
				parts[idx] = strings.ToUpper(part[:1]) + part[1:]
			}
		}
		return strings.Join(parts, "+")
	}

	keybindings := GetKeybindings()
	previousKey := formatKey(keybindings.GetKeys(KeybindingTuiAltScreenSearchPrevious))
	nextKey := formatKey(keybindings.GetKeys(KeybindingTuiAltScreenSearchNext))

	query := c.input.GetValue()
	result := ""
	if query != "" {
		if c.resultCount == 0 {
			result = "No matches"
		} else {
			result = fmt.Sprintf("%d/%d", c.resultIndex+1, c.resultCount)
		}
	}

	resultSpace := innerWidth - 3
	if resultSpace < 0 {
		resultSpace = 0
	}
	visibleResult := TruncateToWidth(result, resultSpace, "", false)
	resultText := ""
	if visibleResult != "" {
		resultText = "\x1b[2m " + visibleResult + " \x1b[22m"
	}

	inputWidth := innerWidth - VisibleWidth(resultText)
	if inputWidth < 0 {
		inputWidth = 0
	}
	renderWidth := inputWidth
	if renderWidth < 1 {
		renderWidth = 1
	}
	renderedInput := c.input.Render(renderWidth)
	firstInputLine := ""
	if len(renderedInput) > 0 {
		firstInputLine = renderedInput[0]
	}
	inputLine := TruncateToWidth(firstInputLine, inputWidth, "", false)
	padCount := inputWidth - VisibleWidth(inputLine)
	if padCount < 0 {
		padCount = 0
	}
	content := inputLine + strings.Repeat(" ", padCount) + resultText

	previousButton := "↑ " + previousKey
	nextButton := "↓ " + nextKey
	separator := " · "
	const outerGapWidth = 1
	availableControlsWidth := innerWidth - outerGapWidth*2 - 1
	if availableControlsWidth < 0 {
		availableControlsWidth = 0
	}
	controlsWidth := VisibleWidth(previousButton) + VisibleWidth(separator) + VisibleWidth(nextButton)
	if controlsWidth > availableControlsWidth {
		previousButton = "↑"
		nextButton = "↓"
		separator = " "
		controlsWidth = VisibleWidth(previousButton) + VisibleWidth(separator) + VisibleWidth(nextButton)
	}

	showButtons := controlsWidth <= availableControlsWidth
	renderedButtons := ""
	if showButtons {
		prevHovered := c.hoveredNavigationDirection != nil && *c.hoveredNavigationDirection == -1
		nextHovered := c.hoveredNavigationDirection != nil && *c.hoveredNavigationDirection == 1
		renderedButtons = c.navigationButtonStyle(previousButton, prevHovered) +
			separator +
			c.navigationButtonStyle(nextButton, nextHovered)
	}

	outerGapsWidth := 0
	if showButtons {
		outerGapsWidth = outerGapWidth * 2
	}
	rightRuleWidth := 0
	if renderedButtons != "" && innerWidth > controlsWidth+outerGapsWidth {
		rightRuleWidth = 1
	}
	usedForButtons := 0
	if showButtons {
		usedForButtons = controlsWidth
	}
	leftRuleWidth := innerWidth - usedForButtons - outerGapsWidth - rightRuleWidth
	if leftRuleWidth < 0 {
		leftRuleWidth = 0
	}

	previousStart := 1 + leftRuleWidth + outerGapWidth
	if showButtons {
		c.previousButtonStart = previousStart
		c.previousButtonEnd = previousStart + VisibleWidth(previousButton)
		c.nextButtonStart = c.previousButtonEnd + VisibleWidth(separator)
		c.nextButtonEnd = c.nextButtonStart + VisibleWidth(nextButton)
	} else {
		c.previousButtonStart = -1
		c.previousButtonEnd = -1
		c.nextButtonStart = -1
		c.nextButtonEnd = -1
	}

	if safeWidth == 1 {
		return []string{"┌", "│", "└"}
	}
	btnPad := ""
	if renderedButtons != "" {
		btnPad = " "
	}
	return []string{
		"┌" + strings.Repeat("─", innerWidth) + "┐",
		"│" + content + "│",
		"└" + strings.Repeat("─", leftRuleWidth) + btnPad + renderedButtons + btnPad + strings.Repeat("─", rightRuleWidth) + "┘",
	}
}

var (
	_ Component = (*AltScreenSearchComponent)(nil)
	_ Focusable = (*AltScreenSearchComponent)(nil)
)
