// Ported from packages/tui/src/utils.ts (pi v1.0.0).

package tui

import "github.com/keejkrej/pi-go/internal/jsre"

// SegmentData is one Intl.SegmentData record.
// Index is the UTF-16 code-unit offset of Segment in Input, matching JS string
// indices, not a Go byte offset.
// IsWordLike is nil when the segmenter omits it (grapheme granularity). Word
// granularity sets true or false.
type SegmentData struct {
	Segment    string `json:"segment"`
	Index      int    `json:"index"`
	Input      string `json:"input"`
	IsWordLike *bool  `json:"isWordLike,omitzero"`
}

// Segmenter is the Intl.Segmenter surface tui uses.
// Segment returns segmenter.segment(input) in order.
type Segmenter interface {
	Segment(input string) []SegmentData
}

// GetGraphemeSegmenter returns the shared grapheme Intl.Segmenter.
func GetGraphemeSegmenter() Segmenter {
	panic("unported: GetGraphemeSegmenter")
}

// GetWordSegmenter returns the shared word Intl.Segmenter.
func GetWordSegmenter() Segmenter {
	panic("unported: GetWordSegmenter")
}

// CjkBreakRegex matches Han, Hiragana, Katakana, Hangul, or Bopomofo script extensions (flag u).
var CjkBreakRegex = jsre.MustCompile(`[\p{Script_Extensions=Han}\p{Script_Extensions=Hiragana}\p{Script_Extensions=Katakana}\p{Script_Extensions=Hangul}\p{Script_Extensions=Bopomofo}]`, "u")

// CjkPunctuationRegex matches CJK punctuation that separates prose from completions (flag u).
var CjkPunctuationRegex = jsre.MustCompile(`(?:(?=\p{Punctuation})`+CjkBreakRegex.Source()+`|[，．：；！？（）［］｛｝“”‘’…—])`, "u")

// AutocompleteSeparatorRegex matches whitespace or CjkPunctuationRegex (flag u).
var AutocompleteSeparatorRegex = jsre.MustCompile(`(?:\s|`+CjkPunctuationRegex.Source()+`)`, "u")

// AutocompleteBoundaryRegex matches the start of text or AutocompleteSeparatorRegex (flag u).
var AutocompleteBoundaryRegex = jsre.MustCompile(`(?:^|`+AutocompleteSeparatorRegex.Source()+`)`, "u")

// PunctuationRegex matches ASCII punctuation used as a word boundary inside a word-like segment.
var PunctuationRegex = jsre.MustCompile(`[(){}[\]<>.,;:'"!?+\-=*/\\|&%^$#@~`+"`"+`]`, "")

// VisibleWidth returns the visible width of str in terminal columns.
// Base code-point width is eastasian.Width with ambiguousAsWide false. Tabs are
// 3 columns. ANSI, OSC, and APC sequences are zero. RGI emoji and regional
// indicators are 2.
func VisibleWidth(str string) int {
	panic("unported: VisibleWidth")
}

// StripTerminalSequences removes ANSI, OSC, and APC sequences and keeps visible text.
func StripTerminalSequences(str string) string {
	panic("unported: StripTerminalSequences")
}

// GraphemeCellRange is the terminal-cell range of the grapheme at a visible column.
// TS leaves the interface unexported; it is exported here as the result of GetGraphemeCellRange.
type GraphemeCellRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// GetGraphemeCellRange returns the cell range of the grapheme covering column.
// Nil means column is not on a visible grapheme.
func GetGraphemeCellRange(line string, column int) *GraphemeCellRange {
	panic("unported: GetGraphemeCellRange")
}

// GetOsc8LinkAtColumn returns the OSC 8 URL covering column.
// Nil means the column is not inside a hyperlink.
func GetOsc8LinkAtColumn(line string, column int) *string {
	panic("unported: GetOsc8LinkAtColumn")
}

// NormalizeTerminalOutput rewrites terminal output without changing logical editor text.
// Thai and Lao AM vowels are compatibility-decomposed, and visible tabs expand to three spaces.
// Tabs inside terminal sequences are left unchanged.
func NormalizeTerminalOutput(str string) string {
	panic("unported: NormalizeTerminalOutput")
}

// ExtractedAnsiCode is an ANSI, OSC, or APC sequence at a string index.
type ExtractedAnsiCode struct {
	Code   string `json:"code"`
	Length int    `json:"length"`
}

// ExtractAnsiCode returns the escape sequence starting at pos, a UTF-16 index.
// Nil means pos does not start a supported sequence.
func ExtractAnsiCode(str string, pos int) *ExtractedAnsiCode {
	panic("unported: ExtractAnsiCode")
}

// GetActiveBackgroundAnsi returns the background SGR sequence active at the end of text.
// The result is empty when no background is active.
func GetActiveBackgroundAnsi(text string) string {
	panic("unported: GetActiveBackgroundAnsi")
}

// FlattenLines forces V8 to flatten concatenated strings before they are cached.
// Go strings are already contiguous, and the TS function does not change values, so this is a no-op.
func FlattenLines(lines []string) {}

// WrapTextWithAnsi word-wraps text to width visible columns, preserving ANSI codes across breaks.
// It does not pad and does not paint a background. width is a column count.
func WrapTextWithAnsi(text string, width int) []string {
	panic("unported: WrapTextWithAnsi")
}

// IsWhitespaceChar reports whether char matches JS \s.
func IsWhitespaceChar(char string) bool {
	panic("unported: IsWhitespaceChar")
}

// IsPunctuationChar reports whether char matches PunctuationRegex.
func IsPunctuationChar(char string) bool {
	panic("unported: IsPunctuationChar")
}

// ApplyBackgroundToLine pads line to width with spaces and passes the result through bgFn.
func ApplyBackgroundToLine(line string, width int, bgFn func(text string) string) string {
	panic("unported: ApplyBackgroundToLine")
}

// TruncateToWidth truncates text to maxWidth visible columns.
// ellipsis is appended when text is cut; the TS default is "...".
// pad pads with spaces to exactly maxWidth; the TS default is false.
func TruncateToWidth(text string, maxWidth int, ellipsis string, pad bool) string {
	panic("unported: TruncateToWidth")
}

// SliceByColumn returns length visible columns of line starting at startCol.
// strict drops a wide character that would extend past the range; the TS default is false.
func SliceByColumn(line string, startCol int, length int, strict bool) string {
	panic("unported: SliceByColumn")
}

// SliceWithWidthResult is the text and visible width returned by SliceWithWidth.
type SliceWithWidthResult struct {
	Text  string `json:"text"`
	Width int    `json:"width"`
}

// SliceWithWidth is SliceByColumn plus the visible width of the extracted text.
// strict drops a wide character that would extend past the range; the TS default is false.
func SliceWithWidth(line string, startCol int, length int, strict bool) SliceWithWidthResult {
	panic("unported: SliceWithWidth")
}

// ExtractSegmentsResult is the content before and after an overlay column range.
type ExtractSegmentsResult struct {
	Before      string `json:"before"`
	BeforeWidth int    `json:"beforeWidth"`
	After       string `json:"after"`
	AfterWidth  int    `json:"afterWidth"`
}

// ExtractSegments splits line into the columns before beforeEnd and the columns
// from afterStart for afterLen. strictAfter drops a wide character past the after range;
// the TS default is false. after inherits SGR and OSC 8 state active at afterStart.
func ExtractSegments(line string, beforeEnd int, afterStart int, afterLen int, strictAfter bool) ExtractSegmentsResult {
	panic("unported: ExtractSegments")
}
