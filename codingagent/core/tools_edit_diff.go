// Ported from packages/coding-agent/src/core/tools/edit-diff.ts (pi v1.0.0).

package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsdiff"
	"golang.org/x/text/unicode/norm"
)

const tedDiffWindowMarginLines = 32

var (
	tedSmartSingleQuotesRe = regexp.MustCompile("[\u2018\u2019\u201A\u201B]")
	tedSmartDoubleQuotesRe = regexp.MustCompile("[\u201C\u201D\u201E\u201F]")
	tedDashesRe            = regexp.MustCompile("[\u2010\u2011\u2012\u2013\u2014\u2015\u2212]")
	tedSpecialSpacesRe     = regexp.MustCompile("[\u00A0\u2002-\u200A\u202F\u205F\u3000]")
	tedLinesWithEndingRe   = regexp.MustCompile(`[^\n]*\n|[^\n]+`)
)

// EditReplacement is one oldText/newText replacement pair for edit operations.
type EditReplacement struct {
	OldText string `json:"oldText"`
	NewText string `json:"newText"`
}

// TextReplacement replaces MatchLength bytes at MatchIndex with NewText.
type TextReplacement struct {
	MatchIndex  int
	MatchLength int
	NewText     string
}

// FuzzyMatchResult describes the match position and normalization mode found by FuzzyFindText.
type FuzzyMatchResult struct {
	Found                 bool
	Index                 int
	MatchLength           int
	UsedFuzzyMatch        bool
	ContentForReplacement string
}

// AppliedEditsResult holds LF-normalized before/after contents and sorted replacements.
type AppliedEditsResult struct {
	BaseContent  string
	NewContent   string
	Replacements []TextReplacement
}

// EditDiff holds the display diff, unified patch, and first changed line of an edit.
type EditDiff struct {
	Diff             string `json:"diff"`
	Patch            string `json:"patch"`
	FirstChangedLine *int   `json:"firstChangedLine"`
}

// DiffStringResult holds a display diff string and its optional first changed line.
type DiffStringResult struct {
	Diff             string `json:"diff"`
	FirstChangedLine *int   `json:"firstChangedLine"`
}

// EditDiffComputeResult represents the discriminated outcome of ComputeEditDiff / ComputeEditsDiff.
type EditDiffComputeResult struct {
	Diff             string
	FirstChangedLine *int
	Error            string
}

type tedLineSpan struct {
	start int
	end   int
}

type tedMatchedEdit struct {
	editIndex   int
	matchIndex  int
	matchLength int
	newText     string
}

type tedDiffWindow struct {
	oldStart int
	oldEnd   int
	oldLine  int
	newStart int
	newEnd   int
	newLine  int
}

// DetectLineEnding returns the dominant line ending ("\r\n" or "\n") in content.
func DetectLineEnding(content string) string {
	crlfIdx := strings.Index(content, "\r\n")
	lfIdx := strings.IndexByte(content, '\n')
	if lfIdx == -1 {
		return "\n"
	}
	if crlfIdx == -1 {
		return "\n"
	}
	if crlfIdx < lfIdx {
		return "\r\n"
	}
	return "\n"
}

// NormalizeToLF converts CRLF and standalone CR line endings to LF.
func NormalizeToLF(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.ReplaceAll(text, "\r", "\n")
}

// RestoreLineEndings converts LF line endings to lineEnding when it is "\r\n".
func RestoreLineEndings(text, lineEnding string) string {
	if lineEnding == "\r\n" {
		return strings.ReplaceAll(text, "\n", "\r\n")
	}
	return text
}

func tedGetLineEndingAt(content string, offset int) string {
	lineFeed := -1
	if offset >= 0 && offset <= len(content) {
		if idx := strings.IndexByte(content[offset:], '\n'); idx != -1 {
			lineFeed = offset + idx
		}
	}
	if lineFeed == -1 {
		searchEnd := min(offset, len(content))
		lineFeed = strings.LastIndexByte(content[:searchEnd], '\n')
	}
	if lineFeed == -1 {
		if strings.ContainsRune(content, '\r') {
			return "\r"
		}
		return "\n"
	}
	if lineFeed > 0 && content[lineFeed-1] == '\r' {
		return "\r\n"
	}
	return "\n"
}

// ApplyReplacementsToOriginal applies replacements matched in NormalizeToLF(originalContent)
// back onto originalContent while preserving unchanged bytes and original per-line endings.
func ApplyReplacementsToOriginal(originalContent string, replacements []TextReplacement) string {
	crlfBefore := 0
	nextCrlf := strings.Index(originalContent, "\r\n")
	toOriginal := func(offset int) int {
		for nextCrlf != -1 && nextCrlf-crlfBefore < offset {
			crlfBefore++
			if nextCrlf+2 <= len(originalContent) {
				if idx := strings.Index(originalContent[nextCrlf+2:], "\r\n"); idx != -1 {
					nextCrlf = nextCrlf + 2 + idx
					continue
				}
			}
			nextCrlf = -1
		}
		return offset + crlfBefore
	}

	var parts []string
	endsWithCr := false
	push := func(part string) {
		if len(part) == 0 {
			return
		}
		if endsWithCr && part[0] == '\n' {
			parts = append(parts, "\n")
		}
		parts = append(parts, part)
		endsWithCr = part[len(part)-1] == '\r'
	}

	position := 0
	for _, repl := range replacements {
		start := toOriginal(repl.MatchIndex)
		if start > position && start <= len(originalContent) {
			push(originalContent[position:start])
		}
		ending := tedGetLineEndingAt(originalContent, start)
		if ending == "\n" {
			push(repl.NewText)
		} else {
			push(strings.ReplaceAll(repl.NewText, "\n", ending))
		}
		position = toOriginal(repl.MatchIndex + repl.MatchLength)
	}
	if position < len(originalContent) {
		push(originalContent[position:])
	}
	return strings.Join(parts, "")
}

// NormalizeForFuzzyMatch normalizes NFKC compatibility forms, strips per-line trailing whitespace,
// and folds smart quotes, Unicode dashes, and special Unicode spaces to ASCII equivalents.
func NormalizeForFuzzyMatch(text string) string {
	nfkc := norm.NFKC.String(text)
	lines := strings.Split(nfkc, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t\f\v\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff")
	}
	joined := strings.Join(lines, "\n")
	joined = tedSmartSingleQuotesRe.ReplaceAllString(joined, "'")
	joined = tedSmartDoubleQuotesRe.ReplaceAllString(joined, `"`)
	joined = tedDashesRe.ReplaceAllString(joined, "-")
	joined = tedSpecialSpacesRe.ReplaceAllString(joined, " ")
	return joined
}

func tedSplitLinesWithEndings(content string) []string {
	if content == "" {
		return nil
	}
	return tedLinesWithEndingRe.FindAllString(content, -1)
}

func tedGetLineSpans(content string) []tedLineSpan {
	lines := tedSplitLinesWithEndings(content)
	spans := make([]tedLineSpan, len(lines))
	offset := 0
	for i, line := range lines {
		spans[i] = tedLineSpan{start: offset, end: offset + len(line)}
		offset += len(line)
	}
	return spans
}

func tedGetReplacementLineRange(lines []tedLineSpan, repl TextReplacement) (int, int, error) {
	replStart := repl.MatchIndex
	replEnd := repl.MatchIndex + repl.MatchLength

	startLine := -1
	for i, line := range lines {
		if replStart >= line.start && replStart < line.end {
			startLine = i
			break
		}
	}
	if startLine == -1 {
		return 0, 0, errors.New("Replacement range is outside the base content.")
	}

	endLine := startLine
	for endLine < len(lines) && lines[endLine].end < replEnd {
		endLine++
	}
	if endLine >= len(lines) {
		return 0, 0, errors.New("Replacement range is outside the base content.")
	}
	return startLine, endLine + 1, nil
}

func tedApplyReplacements(content string, replacements []TextReplacement, offset int) string {
	result := content
	for i := len(replacements) - 1; i >= 0; i-- {
		repl := replacements[i]
		idx := repl.MatchIndex - offset
		result = result[:idx] + repl.NewText + result[idx+repl.MatchLength:]
	}
	return result
}

func tedWidenReplacementsToLines(originalContent, baseContent string, replacements []TextReplacement) ([]TextReplacement, error) {
	originalLines := tedGetLineSpans(originalContent)
	baseLines := tedGetLineSpans(baseContent)
	if len(originalLines) != len(baseLines) {
		return nil, errors.New("Cannot preserve unchanged lines because the base content has a different line count.")
	}

	sortedRepls := slices.Clone(replacements)
	sort.Slice(sortedRepls, func(i, j int) bool {
		return sortedRepls[i].MatchIndex < sortedRepls[j].MatchIndex
	})

	type group struct {
		startLine    int
		endLine      int
		replacements []TextReplacement
	}
	var groups []group
	for _, repl := range sortedRepls {
		startLine, endLine, err := tedGetReplacementLineRange(baseLines, repl)
		if err != nil {
			return nil, err
		}
		if len(groups) > 0 && startLine < groups[len(groups)-1].endLine {
			last := &groups[len(groups)-1]
			if endLine > last.endLine {
				last.endLine = endLine
			}
			last.replacements = append(last.replacements, repl)
			continue
		}
		groups = append(groups, group{
			startLine:    startLine,
			endLine:      endLine,
			replacements: []TextReplacement{repl},
		})
	}

	out := make([]TextReplacement, len(groups))
	for i, g := range groups {
		groupStartOffset := baseLines[g.startLine].start
		groupEndOffset := baseLines[g.endLine-1].end
		originalStart := originalLines[g.startLine].start
		originalEnd := originalLines[g.endLine-1].end
		out[i] = TextReplacement{
			MatchIndex:  originalStart,
			MatchLength: originalEnd - originalStart,
			NewText:     tedApplyReplacements(baseContent[groupStartOffset:groupEndOffset], g.replacements, groupStartOffset),
		}
	}
	return out, nil
}

// FuzzyFindText locates oldText in content, preferring an exact byte match and falling back to fuzzy normalization.
func FuzzyFindText(content, oldText string) FuzzyMatchResult {
	if exactIndex := strings.Index(content, oldText); exactIndex != -1 {
		return FuzzyMatchResult{
			Found:                 true,
			Index:                 exactIndex,
			MatchLength:           len(oldText),
			UsedFuzzyMatch:        false,
			ContentForReplacement: content,
		}
	}

	fuzzyContent := NormalizeForFuzzyMatch(content)
	fuzzyOldText := NormalizeForFuzzyMatch(oldText)
	if fuzzyOldText == "" {
		if fuzzyIndex := strings.Index(fuzzyContent, fuzzyOldText); fuzzyIndex != -1 {
			return FuzzyMatchResult{
				Found:                 true,
				Index:                 fuzzyIndex,
				MatchLength:           0,
				UsedFuzzyMatch:        true,
				ContentForReplacement: fuzzyContent,
			}
		}
	}
	fuzzyIndex := strings.Index(fuzzyContent, fuzzyOldText)
	if fuzzyIndex == -1 {
		return FuzzyMatchResult{
			Found:                 false,
			Index:                 -1,
			MatchLength:           0,
			UsedFuzzyMatch:        false,
			ContentForReplacement: content,
		}
	}
	return FuzzyMatchResult{
		Found:                 true,
		Index:                 fuzzyIndex,
		MatchLength:           len(fuzzyOldText),
		UsedFuzzyMatch:        true,
		ContentForReplacement: fuzzyContent,
	}
}

func tedCountOccurrences(content, oldText string) int {
	fuzzyContent := NormalizeForFuzzyMatch(content)
	fuzzyOldText := NormalizeForFuzzyMatch(oldText)
	if fuzzyOldText == "" {
		return 0
	}
	return strings.Count(fuzzyContent, fuzzyOldText)
}

func tedGetNotFoundError(filePath string, editIndex, totalEdits int) error {
	if totalEdits == 1 {
		return fmt.Errorf("Could not find the exact text in %s. The old text must match exactly including all whitespace and newlines.", filePath)
	}
	return fmt.Errorf("Could not find edits[%d] in %s. The oldText must match exactly including all whitespace and newlines.", editIndex, filePath)
}

func tedGetDuplicateError(filePath string, editIndex, totalEdits, occurrences int) error {
	if totalEdits == 1 {
		return fmt.Errorf("Found %d occurrences of the text in %s. The text must be unique. Please provide more context to make it unique.", occurrences, filePath)
	}
	return fmt.Errorf("Found %d occurrences of edits[%d] in %s. Each oldText must be unique. Please provide more context to make it unique.", occurrences, editIndex, filePath)
}

func tedGetEmptyOldTextError(filePath string, editIndex, totalEdits int) error {
	if totalEdits == 1 {
		return fmt.Errorf("oldText must not be empty in %s.", filePath)
	}
	return fmt.Errorf("edits[%d].oldText must not be empty in %s.", editIndex, filePath)
}

func tedGetNoChangeError(filePath string, totalEdits int) error {
	if totalEdits == 1 {
		return fmt.Errorf("No changes made to %s. The replacement produced identical content. This might indicate an issue with special characters or the text not existing as expected.", filePath)
	}
	return fmt.Errorf("No changes made to %s. The replacements produced identical content.", filePath)
}

// ApplyEditsToNormalizedContent matches and applies a batch of disjoint edits to LF-normalized content.
func ApplyEditsToNormalizedContent(normalizedContent string, edits []EditReplacement, filePath string) (AppliedEditsResult, error) {
	normalizedEdits := make([]EditReplacement, len(edits))
	for i, e := range edits {
		normalizedEdits[i] = EditReplacement{
			OldText: NormalizeToLF(e.OldText),
			NewText: NormalizeToLF(e.NewText),
		}
	}

	for i, e := range normalizedEdits {
		if len(e.OldText) == 0 {
			return AppliedEditsResult{}, tedGetEmptyOldTextError(filePath, i, len(normalizedEdits))
		}
	}

	usedFuzzyMatch := false
	for _, e := range normalizedEdits {
		if FuzzyFindText(normalizedContent, e.OldText).UsedFuzzyMatch {
			usedFuzzyMatch = true
			break
		}
	}

	replacementBaseContent := normalizedContent
	if usedFuzzyMatch {
		replacementBaseContent = NormalizeForFuzzyMatch(normalizedContent)
	}

	matchedEdits := make([]tedMatchedEdit, 0, len(normalizedEdits))
	for i, e := range normalizedEdits {
		matchResult := FuzzyFindText(replacementBaseContent, e.OldText)
		if !matchResult.Found {
			return AppliedEditsResult{}, tedGetNotFoundError(filePath, i, len(normalizedEdits))
		}
		occurrences := tedCountOccurrences(replacementBaseContent, e.OldText)
		if occurrences > 1 {
			return AppliedEditsResult{}, tedGetDuplicateError(filePath, i, len(normalizedEdits), occurrences)
		}
		matchedEdits = append(matchedEdits, tedMatchedEdit{
			editIndex:   i,
			matchIndex:  matchResult.Index,
			matchLength: matchResult.MatchLength,
			newText:     e.NewText,
		})
	}

	sort.Slice(matchedEdits, func(i, j int) bool {
		return matchedEdits[i].matchIndex < matchedEdits[j].matchIndex
	})

	for i := 1; i < len(matchedEdits); i++ {
		prev := matchedEdits[i-1]
		curr := matchedEdits[i]
		if prev.matchIndex+prev.matchLength > curr.matchIndex {
			return AppliedEditsResult{}, fmt.Errorf(
				"edits[%d] and edits[%d] overlap in %s. Merge them into one edit or target disjoint regions.",
				prev.editIndex, curr.editIndex, filePath,
			)
		}
	}

	rawRepls := make([]TextReplacement, len(matchedEdits))
	for i, m := range matchedEdits {
		rawRepls[i] = TextReplacement{
			MatchIndex:  m.matchIndex,
			MatchLength: m.matchLength,
			NewText:     m.newText,
		}
	}

	var replacements []TextReplacement
	if usedFuzzyMatch {
		var err error
		replacements, err = tedWidenReplacementsToLines(normalizedContent, replacementBaseContent, rawRepls)
		if err != nil {
			return AppliedEditsResult{}, err
		}
	} else {
		replacements = rawRepls
	}

	newContent := tedApplyReplacements(normalizedContent, replacements, 0)
	if normalizedContent == newContent {
		return AppliedEditsResult{}, tedGetNoChangeError(filePath, len(normalizedEdits))
	}

	return AppliedEditsResult{
		BaseContent:  normalizedContent,
		NewContent:   newContent,
		Replacements: replacements,
	}, nil
}

// GenerateUnifiedPatch generates a standard unified patch with file headers only.
func GenerateUnifiedPatch(filePath, oldContent, newContent string, contextLines ...int) string {
	ctxLines := 4
	if len(contextLines) > 0 {
		ctxLines = contextLines[0]
	}
	return jsdiff.CreateTwoFilesPatch(filePath, filePath, oldContent, newContent, nil, nil, &jsdiff.PatchOptions{
		Context:       &ctxLines,
		HeaderOptions: &jsdiff.FileHeadersOnly,
	})
}

func tedCountLines(text string) int {
	return tedCountLineFeeds(text, 0, len(text)) + 1
}

func tedCountLineFeeds(text string, start, end int) int {
	if start < 0 {
		start = 0
	}
	if end > len(text) {
		end = len(text)
	}
	if start >= end {
		return 0
	}
	return strings.Count(text[start:end], "\n")
}

func tedPadLineNum(n, width int) string {
	s := strconv.Itoa(n)
	if len(s) >= width {
		return s
	}
	return strings.Repeat(" ", width-len(s)) + s
}

func tedFormatDisplayDiff(parts []jsdiff.Change, oldStartLine, newStartLine, lineNumWidth, contextLines int) ([]string, *int) {
	var output []string
	oldLineNum := oldStartLine
	newLineNum := newStartLine
	lastWasChange := false
	var firstChangedLine *int

	for i, part := range parts {
		raw := strings.Split(part.Value, "\n")
		if len(raw) > 0 && raw[len(raw)-1] == "" {
			raw = raw[:len(raw)-1]
		}

		if part.Added || part.Removed {
			if firstChangedLine == nil {
				lineCopy := newLineNum
				firstChangedLine = &lineCopy
			}
			for _, line := range raw {
				if part.Added {
					output = append(output, fmt.Sprintf("+%s %s", tedPadLineNum(newLineNum, lineNumWidth), line))
					newLineNum++
				} else {
					output = append(output, fmt.Sprintf("-%s %s", tedPadLineNum(oldLineNum, lineNumWidth), line))
					oldLineNum++
				}
			}
			lastWasChange = true
		} else {
			nextPartIsChange := i < len(parts)-1 && (parts[i+1].Added || parts[i+1].Removed)
			hasLeadingChange := lastWasChange
			hasTrailingChange := nextPartIsChange

			switch {
			case hasLeadingChange && hasTrailingChange:
				if len(raw) <= contextLines*2 {
					for _, line := range raw {
						output = append(output, fmt.Sprintf(" %s %s", tedPadLineNum(oldLineNum, lineNumWidth), line))
						oldLineNum++
						newLineNum++
					}
				} else {
					leadingLines := raw[:contextLines]
					trailingLines := raw[len(raw)-contextLines:]
					skippedLines := len(raw) - len(leadingLines) - len(trailingLines)

					for _, line := range leadingLines {
						output = append(output, fmt.Sprintf(" %s %s", tedPadLineNum(oldLineNum, lineNumWidth), line))
						oldLineNum++
						newLineNum++
					}
					output = append(output, fmt.Sprintf(" %s ...", strings.Repeat(" ", lineNumWidth)))
					oldLineNum += skippedLines
					newLineNum += skippedLines
					for _, line := range trailingLines {
						output = append(output, fmt.Sprintf(" %s %s", tedPadLineNum(oldLineNum, lineNumWidth), line))
						oldLineNum++
						newLineNum++
					}
				}
			case hasLeadingChange:
				shownCount := min(contextLines, len(raw))
				shownLines := raw[:shownCount]
				skippedLines := len(raw) - len(shownLines)
				for _, line := range shownLines {
					output = append(output, fmt.Sprintf(" %s %s", tedPadLineNum(oldLineNum, lineNumWidth), line))
					oldLineNum++
					newLineNum++
				}
				if skippedLines > 0 {
					output = append(output, fmt.Sprintf(" %s ...", strings.Repeat(" ", lineNumWidth)))
					oldLineNum += skippedLines
					newLineNum += skippedLines
				}
			case hasTrailingChange:
				skippedLines := max(0, len(raw)-contextLines)
				if skippedLines > 0 {
					output = append(output, fmt.Sprintf(" %s ...", strings.Repeat(" ", lineNumWidth)))
					oldLineNum += skippedLines
					newLineNum += skippedLines
				}
				for _, line := range raw[skippedLines:] {
					output = append(output, fmt.Sprintf(" %s %s", tedPadLineNum(oldLineNum, lineNumWidth), line))
					oldLineNum++
					newLineNum++
				}
			default:
				oldLineNum += len(raw)
				newLineNum += len(raw)
			}

			lastWasChange = false
		}
	}
	return output, firstChangedLine
}

// GenerateDiffString generates a display-oriented diff string with line numbers and context.
func GenerateDiffString(oldContent, newContent string, contextLines ...int) DiffStringResult {
	ctxLines := 4
	if len(contextLines) > 0 {
		ctxLines = contextLines[0]
	}
	lineNumWidth := len(strconv.Itoa(max(tedCountLines(oldContent), tedCountLines(newContent))))
	output, firstChangedLine := tedFormatDisplayDiff(
		jsdiff.DiffLines(oldContent, newContent, nil),
		1, 1, lineNumWidth, ctxLines,
	)
	return DiffStringResult{
		Diff:             strings.Join(output, "\n"),
		FirstChangedLine: firstChangedLine,
	}
}

func tedLineStartBefore(text string, offset int) int {
	if offset <= 0 {
		return 0
	}
	idx := strings.LastIndexByte(text[:offset], '\n')
	return idx + 1
}

func tedGetDiffWindows(baseContent string, replacements []TextReplacement) []tedDiffWindow {
	var windows []tedDiffWindow
	offsetDelta := 0
	lineDelta := 0
	cursorOffset := 0
	cursorLine := 0

	for _, repl := range replacements {
		matchEnd := repl.MatchIndex + repl.MatchLength
		start := tedLineStartBefore(baseContent, repl.MatchIndex)
		for i := 0; i < tedDiffWindowMarginLines && start > 0; i++ {
			start = tedLineStartBefore(baseContent, start-1)
		}
		end := matchEnd
		for i := 0; i <= tedDiffWindowMarginLines && end < len(baseContent); i++ {
			searchFrom := end
			if i == 0 {
				searchFrom = max(matchEnd-1, repl.MatchIndex)
			}
			if searchFrom >= len(baseContent) {
				end = len(baseContent)
				break
			}
			if idx := strings.IndexByte(baseContent[searchFrom:], '\n'); idx == -1 {
				end = len(baseContent)
			} else {
				end = searchFrom + idx + 1
			}
		}

		replOffsetDelta := len(repl.NewText) - repl.MatchLength
		replLineDelta := tedCountLineFeeds(repl.NewText, 0, len(repl.NewText)) - tedCountLineFeeds(baseContent, repl.MatchIndex, matchEnd)

		if len(windows) > 0 && start <= windows[len(windows)-1].oldEnd {
			prev := &windows[len(windows)-1]
			if end > prev.oldEnd {
				prev.oldEnd = end
			}
			prev.newEnd = prev.oldEnd + offsetDelta + replOffsetDelta
		} else {
			cursorLine += tedCountLineFeeds(baseContent, cursorOffset, start)
			cursorOffset = start
			windows = append(windows, tedDiffWindow{
				oldStart: start,
				oldEnd:   end,
				oldLine:  cursorLine,
				newStart: start + offsetDelta,
				newEnd:   end + offsetDelta + replOffsetDelta,
				newLine:  cursorLine + lineDelta,
			})
		}
		offsetDelta += replOffsetDelta
		lineDelta += replLineDelta
	}
	return windows
}

// GenerateEditDiff computes the display diff and unified patch only in bounded windows around replacements.
func GenerateEditDiff(filePath, baseContent, newContent string, replacements []TextReplacement, contextLines ...int) EditDiff {
	ctxLines := 4
	if len(contextLines) > 0 {
		ctxLines = contextLines[0]
	}
	lineNumWidth := len(strconv.Itoa(max(tedCountLines(baseContent), tedCountLines(newContent))))
	skipMarker := fmt.Sprintf(" %s ...", strings.Repeat(" ", lineNumWidth))
	var output []string
	var hunks []jsdiff.StructuredPatchHunk
	var firstChangedLine *int

	for _, w := range tedGetDiffWindows(baseContent, replacements) {
		oldText := baseContent[w.oldStart:w.oldEnd]
		newText := newContent[w.newStart:w.newEnd]
		shownOutput, shownFirst := tedFormatDisplayDiff(
			jsdiff.DiffLines(oldText, newText, nil),
			w.oldLine+1, w.newLine+1, lineNumWidth, ctxLines,
		)
		if firstChangedLine == nil {
			firstChangedLine = shownFirst
		}
		if len(output) > 0 && len(shownOutput) > 0 && output[len(output)-1] == skipMarker && shownOutput[0] == skipMarker {
			shownOutput = shownOutput[1:]
		}
		output = append(output, shownOutput...)

		patch := jsdiff.NewStructuredPatch(filePath, filePath, oldText, newText, nil, nil, &jsdiff.PatchOptions{
			Context: &ctxLines,
		})
		if patch != nil {
			for _, hunk := range patch.Hunks {
				hunk.OldStart += w.oldLine
				hunk.NewStart += w.newLine
				hunks = append(hunks, hunk)
			}
		}
	}

	patchStr := jsdiff.FormatPatch(&jsdiff.StructuredPatch{
		OldFileName: filePath,
		NewFileName: filePath,
		Hunks:       hunks,
	}, &jsdiff.FileHeadersOnly)

	return EditDiff{
		Diff:             strings.Join(output, "\n"),
		Patch:            patchStr,
		FirstChangedLine: firstChangedLine,
	}
}

func tedFormatAccessErrorCode(err error) string {
	switch {
	case errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOENT):
		return "Error code: ENOENT"
	case errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM):
		return "Error code: EACCES"
	case errors.Is(err, syscall.ENOTDIR):
		return "Error code: ENOTDIR"
	case errors.Is(err, syscall.EISDIR):
		return "Error code: EISDIR"
	default:
		var errno syscall.Errno
		if errors.As(err, &errno) {
			return fmt.Sprintf("Error code: %s", errno.Error())
		}
		return err.Error()
	}
}

// ComputeEditsDiff computes the preview diff for one or more edits without mutating the file.
func ComputeEditsDiff(ctx context.Context, filePath string, edits []EditReplacement, cwd string) EditDiffComputeResult {
	if ctx != nil && ctx.Err() != nil {
		return EditDiffComputeResult{Error: ctx.Err().Error()}
	}
	absolutePath := ResolveToCwd(filePath, cwd)

	f, err := os.Open(absolutePath)
	if err != nil {
		return EditDiffComputeResult{
			Error: fmt.Sprintf("Could not edit file: %s. %s.", filePath, tedFormatAccessErrorCode(err)),
		}
	}
	_ = f.Close()

	rawBytes, err := os.ReadFile(absolutePath)
	if err != nil {
		return EditDiffComputeResult{Error: err.Error()}
	}

	split := utils.SplitBom(string(rawBytes))
	normalizedContent := NormalizeToLF(split.Text)
	applied, err := ApplyEditsToNormalizedContent(normalizedContent, edits, filePath)
	if err != nil {
		return EditDiffComputeResult{Error: err.Error()}
	}

	diffRes := GenerateEditDiff(filePath, applied.BaseContent, applied.NewContent, applied.Replacements)
	return EditDiffComputeResult{
		Diff:             diffRes.Diff,
		FirstChangedLine: diffRes.FirstChangedLine,
	}
}

// ComputeEditDiff computes the preview diff for a single edit without mutating the file.
func ComputeEditDiff(ctx context.Context, filePath, oldText, newText, cwd string) EditDiffComputeResult {
	return ComputeEditsDiff(ctx, filePath, []EditReplacement{{OldText: oldText, NewText: newText}}, cwd)
}
