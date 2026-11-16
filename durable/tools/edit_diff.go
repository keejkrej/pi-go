// Ported from packages/durable/src/tools/edit-diff.ts (pi v1.0.0).

package tools

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/keejkrej/pi-go/internal/jsdiff"
	"golang.org/x/text/unicode/norm"
)

var (
	edSmartSingleQuotesRe = regexp.MustCompile("[\u2018\u2019\u201A\u201B]")
	edSmartDoubleQuotesRe = regexp.MustCompile("[\u201C\u201D\u201E\u201F]")
	edDashesRe            = regexp.MustCompile("[\u2010\u2011\u2012\u2013\u2014\u2015\u2212]")
	edSpecialSpacesRe     = regexp.MustCompile("[\u00A0\u2002-\u200A\u202F\u205F\u3000]")
)

// Edit represents one targeted oldText -> newText replacement.
type Edit struct {
	OldText string `json:"oldText"`
	NewText string `json:"newText"`
}

// TextReplacement locates a replacement span inside base content.
type TextReplacement struct {
	MatchIndex  int
	MatchLength int
	NewText     string
}

// FuzzyMatchResult describes an exact or fuzzy match outcome.
type FuzzyMatchResult struct {
	Found                 bool
	Index                 int
	MatchLength           int
	UsedFuzzyMatch        bool
	ContentForReplacement string
}

// AppliedEditsResult holds LF-normalized original and rewritten contents.
type AppliedEditsResult struct {
	BaseContent string
	NewContent  string
}

// DiffStringResult holds a formatted diff string and the 1-indexed first changed line in new content.
type DiffStringResult struct {
	Diff             string
	FirstChangedLine *int
}

type edLineSpan struct {
	start int
	end   int
}

type edMatchedEdit struct {
	editIndex   int
	matchIndex  int
	matchLength int
	newText     string
}

type edReplacementGroup struct {
	startLine    int
	endLine      int
	replacements []TextReplacement
}

// DetectLineEnding detects the dominant first line ending in content ("\r\n" or "\n").
func DetectLineEnding(content string) string {
	crlfIdx := strings.Index(content, "\r\n")
	lfIdx := strings.Index(content, "\n")
	if lfIdx == -1 || crlfIdx == -1 {
		return "\n"
	}
	if crlfIdx < lfIdx {
		return "\r\n"
	}
	return "\n"
}

// NormalizeToLF converts CRLF and CR line endings to LF.
func NormalizeToLF(text string) string {
	return strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
}

// RestoreLineEndings converts LF line endings back to ending ("\r\n" or "\n").
func RestoreLineEndings(text string, ending string) string {
	if ending == "\r\n" {
		return strings.ReplaceAll(text, "\n", "\r\n")
	}
	return text
}

// NormalizeForFuzzyMatch normalizes text using NFKC, trimmed line ends, ASCII quotes/dashes, and standard spaces.
func NormalizeForFuzzyMatch(text string) string {
	normalized := norm.NFKC.String(text)
	lines := strings.Split(normalized, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRightFunc(line, func(r rune) bool {
			return unicode.IsSpace(r) || r == '\uFEFF'
		})
	}
	joined := strings.Join(lines, "\n")
	joined = edSmartSingleQuotesRe.ReplaceAllString(joined, "'")
	joined = edSmartDoubleQuotesRe.ReplaceAllString(joined, "\"")
	joined = edDashesRe.ReplaceAllString(joined, "-")
	joined = edSpecialSpacesRe.ReplaceAllString(joined, " ")
	return joined
}

func edSplitLinesWithEndings(content string) []string {
	if content == "" {
		return nil
	}
	var lines []string
	start := 0
	for i := 0; i < len(content); i++ {
		if content[i] == '\n' {
			lines = append(lines, content[start:i+1])
			start = i + 1
		}
	}
	if start < len(content) {
		lines = append(lines, content[start:])
	}
	return lines
}

func edGetLineSpans(content string) []edLineSpan {
	lines := edSplitLinesWithEndings(content)
	spans := make([]edLineSpan, len(lines))
	offset := 0
	for i, line := range lines {
		spans[i] = edLineSpan{start: offset, end: offset + len(line)}
		offset += len(line)
	}
	return spans
}

func edGetReplacementLineRange(lines []edLineSpan, replacement TextReplacement) (int, int, error) {
	replacementStart := replacement.MatchIndex
	replacementEnd := replacement.MatchIndex + replacement.MatchLength

	startLine := -1
	for i, line := range lines {
		if replacementStart >= line.start && replacementStart < line.end {
			startLine = i
			break
		}
	}
	if startLine == -1 {
		return 0, 0, errors.New("Replacement range is outside the base content.")
	}

	endLine := startLine
	for endLine < len(lines) && lines[endLine].end < replacementEnd {
		endLine++
	}
	if endLine >= len(lines) {
		return 0, 0, errors.New("Replacement range is outside the base content.")
	}
	return startLine, endLine + 1, nil
}

func edApplyReplacements(content string, replacements []TextReplacement, offset int) string {
	result := content
	for i := len(replacements) - 1; i >= 0; i-- {
		replacement := replacements[i]
		matchIndex := replacement.MatchIndex - offset
		result = result[:matchIndex] + replacement.NewText + result[matchIndex+replacement.MatchLength:]
	}
	return result
}

// ApplyReplacementsPreservingUnchangedLines applies normalized-base replacements while copying untouched lines verbatim from originalContent.
func ApplyReplacementsPreservingUnchangedLines(
	originalContent string,
	baseContent string,
	replacements []TextReplacement,
) (string, error) {
	originalLines := edSplitLinesWithEndings(originalContent)
	baseLines := edGetLineSpans(baseContent)
	if len(originalLines) != len(baseLines) {
		return "", errors.New("Cannot preserve unchanged lines because the base content has a different line count.")
	}

	sortedReplacements := make([]TextReplacement, len(replacements))
	copy(sortedReplacements, replacements)
	sort.SliceStable(sortedReplacements, func(i, j int) bool {
		return sortedReplacements[i].MatchIndex < sortedReplacements[j].MatchIndex
	})

	var groups []edReplacementGroup
	for _, replacement := range sortedReplacements {
		startLine, endLine, err := edGetReplacementLineRange(baseLines, replacement)
		if err != nil {
			return "", err
		}
		if len(groups) > 0 && startLine < groups[len(groups)-1].endLine {
			last := &groups[len(groups)-1]
			if endLine > last.endLine {
				last.endLine = endLine
			}
			last.replacements = append(last.replacements, replacement)
			continue
		}
		groups = append(groups, edReplacementGroup{
			startLine:    startLine,
			endLine:      endLine,
			replacements: []TextReplacement{replacement},
		})
	}

	var out strings.Builder
	originalLineIndex := 0
	for _, group := range groups {
		for i := originalLineIndex; i < group.startLine; i++ {
			out.WriteString(originalLines[i])
		}
		groupStartOffset := baseLines[group.startLine].start
		groupEndOffset := baseLines[group.endLine-1].end
		out.WriteString(edApplyReplacements(
			baseContent[groupStartOffset:groupEndOffset],
			group.replacements,
			groupStartOffset,
		))
		originalLineIndex = group.endLine
	}
	for i := originalLineIndex; i < len(originalLines); i++ {
		out.WriteString(originalLines[i])
	}
	return out.String(), nil
}

// FuzzyFindText locates oldText inside content, trying exact match before fuzzy-normalized match.
func FuzzyFindText(content string, oldText string) FuzzyMatchResult {
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

// StripBom removes a leading UTF-8 BOM if present and returns both the BOM and the stripped text.
func StripBom(content string) (string, string) {
	const utf8Bom = "\uFEFF"
	if strings.HasPrefix(content, utf8Bom) {
		return utf8Bom, content[len(utf8Bom):]
	}
	return "", content
}

func edCountOccurrences(content string, oldText string) int {
	fuzzyContent := NormalizeForFuzzyMatch(content)
	fuzzyOldText := NormalizeForFuzzyMatch(oldText)
	return strings.Count(fuzzyContent, fuzzyOldText)
}

func edNotFoundError(path string, editIndex int, totalEdits int) error {
	if totalEdits == 1 {
		return fmt.Errorf("Could not find the exact text in %s. The old text must match exactly including all whitespace and newlines.", path)
	}
	return fmt.Errorf("Could not find edits[%d] in %s. The oldText must match exactly including all whitespace and newlines.", editIndex, path)
}

func edDuplicateError(path string, editIndex int, totalEdits int, occurrences int) error {
	if totalEdits == 1 {
		return fmt.Errorf("Found %d occurrences of the text in %s. The text must be unique. Please provide more context to make it unique.", occurrences, path)
	}
	return fmt.Errorf("Found %d occurrences of edits[%d] in %s. Each oldText must be unique. Please provide more context to make it unique.", occurrences, editIndex, path)
}

func edEmptyOldTextError(path string, editIndex int, totalEdits int) error {
	if totalEdits == 1 {
		return fmt.Errorf("oldText must not be empty in %s.", path)
	}
	return fmt.Errorf("edits[%d].oldText must not be empty in %s.", editIndex, path)
}

func edNoChangeError(path string, totalEdits int) error {
	if totalEdits == 1 {
		return fmt.Errorf("No changes made to %s. The replacement produced identical content. This might indicate an issue with special characters or the text not existing as expected.", path)
	}
	return fmt.Errorf("No changes made to %s. The replacements produced identical content.", path)
}

// ApplyEditsToNormalizedContent matches and applies non-overlapping targeted replacements to LF-normalized text.
func ApplyEditsToNormalizedContent(
	normalizedContent string,
	edits []Edit,
	path string,
) (AppliedEditsResult, error) {
	normalizedEdits := make([]Edit, len(edits))
	for i, edit := range edits {
		normalizedEdits[i] = Edit{
			OldText: NormalizeToLF(edit.OldText),
			NewText: NormalizeToLF(edit.NewText),
		}
	}

	for i, edit := range normalizedEdits {
		if len(edit.OldText) == 0 {
			return AppliedEditsResult{}, edEmptyOldTextError(path, i, len(normalizedEdits))
		}
	}

	usedFuzzyMatch := false
	for _, edit := range normalizedEdits {
		if FuzzyFindText(normalizedContent, edit.OldText).UsedFuzzyMatch {
			usedFuzzyMatch = true
			break
		}
	}
	replacementBaseContent := normalizedContent
	if usedFuzzyMatch {
		replacementBaseContent = NormalizeForFuzzyMatch(normalizedContent)
	}

	matchedEdits := make([]edMatchedEdit, 0, len(normalizedEdits))
	for i, edit := range normalizedEdits {
		matchResult := FuzzyFindText(replacementBaseContent, edit.OldText)
		if !matchResult.Found {
			return AppliedEditsResult{}, edNotFoundError(path, i, len(normalizedEdits))
		}
		occurrences := edCountOccurrences(replacementBaseContent, edit.OldText)
		if occurrences > 1 {
			return AppliedEditsResult{}, edDuplicateError(path, i, len(normalizedEdits), occurrences)
		}
		matchedEdits = append(matchedEdits, edMatchedEdit{
			editIndex:   i,
			matchIndex:  matchResult.Index,
			matchLength: matchResult.MatchLength,
			newText:     edit.NewText,
		})
	}

	sort.SliceStable(matchedEdits, func(i, j int) bool {
		return matchedEdits[i].matchIndex < matchedEdits[j].matchIndex
	})
	for i := 1; i < len(matchedEdits); i++ {
		previous := matchedEdits[i-1]
		current := matchedEdits[i]
		if previous.matchIndex+previous.matchLength > current.matchIndex {
			return AppliedEditsResult{}, fmt.Errorf(
				"edits[%d] and edits[%d] overlap in %s. Merge them into one edit or target disjoint regions.",
				previous.editIndex,
				current.editIndex,
				path,
			)
		}
	}

	replacements := make([]TextReplacement, len(matchedEdits))
	for i, m := range matchedEdits {
		replacements[i] = TextReplacement{
			MatchIndex:  m.matchIndex,
			MatchLength: m.matchLength,
			NewText:     m.newText,
		}
	}

	baseContent := normalizedContent
	var newContent string
	if usedFuzzyMatch {
		var err error
		newContent, err = ApplyReplacementsPreservingUnchangedLines(normalizedContent, replacementBaseContent, replacements)
		if err != nil {
			return AppliedEditsResult{}, err
		}
	} else {
		newContent = edApplyReplacements(replacementBaseContent, replacements, 0)
	}

	if baseContent == newContent {
		return AppliedEditsResult{}, edNoChangeError(path, len(normalizedEdits))
	}

	return AppliedEditsResult{BaseContent: baseContent, NewContent: newContent}, nil
}

// GenerateUnifiedPatch formats a standard unified diff patch with file headers only.
func GenerateUnifiedPatch(path string, oldContent string, newContent string, contextLines ...int) string {
	context := 4
	if len(contextLines) > 0 {
		context = contextLines[0]
	}
	return jsdiff.CreateTwoFilesPatch(
		path,
		path,
		oldContent,
		newContent,
		nil,
		nil,
		&jsdiff.PatchOptions{
			Context:       &context,
			HeaderOptions: &jsdiff.FileHeadersOnly,
		},
	)
}

func edPadLeft(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return strings.Repeat(" ", width-len(s)) + s
}

// GenerateDiffString formats a display diff with aligned line numbers and context truncation.
func GenerateDiffString(oldContent string, newContent string, contextLines ...int) DiffStringResult {
	ctxLines := 4
	if len(contextLines) > 0 {
		ctxLines = contextLines[0]
	}

	parts := jsdiff.DiffLines(oldContent, newContent, nil)
	var output []string

	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")
	maxLineNum := len(oldLines)
	if len(newLines) > maxLineNum {
		maxLineNum = len(newLines)
	}
	lineNumWidth := len(strconv.Itoa(maxLineNum))

	oldLineNum := 1
	newLineNum := 1
	lastWasChange := false
	var firstChangedLine *int

	for i, part := range parts {
		raw := strings.Split(part.Value, "\n")
		if len(raw) > 0 && raw[len(raw)-1] == "" {
			raw = raw[:len(raw)-1]
		}

		if part.Added || part.Removed {
			if firstChangedLine == nil {
				line := newLineNum
				firstChangedLine = &line
			}
			for _, line := range raw {
				if part.Added {
					lineNum := edPadLeft(strconv.Itoa(newLineNum), lineNumWidth)
					output = append(output, fmt.Sprintf("+%s %s", lineNum, line))
					newLineNum++
				} else {
					lineNum := edPadLeft(strconv.Itoa(oldLineNum), lineNumWidth)
					output = append(output, fmt.Sprintf("-%s %s", lineNum, line))
					oldLineNum++
				}
			}
			lastWasChange = true
			continue
		}

		nextPartIsChange := i < len(parts)-1 && (parts[i+1].Added || parts[i+1].Removed)
		hasLeadingChange := lastWasChange
		hasTrailingChange := nextPartIsChange

		switch {
		case hasLeadingChange && hasTrailingChange:
			if len(raw) <= ctxLines*2 {
				for _, line := range raw {
					lineNum := edPadLeft(strconv.Itoa(oldLineNum), lineNumWidth)
					output = append(output, fmt.Sprintf(" %s %s", lineNum, line))
					oldLineNum++
					newLineNum++
				}
			} else {
				leadingLines := raw[:ctxLines]
				trailingLines := raw[len(raw)-ctxLines:]
				skippedLines := len(raw) - len(leadingLines) - len(trailingLines)

				for _, line := range leadingLines {
					lineNum := edPadLeft(strconv.Itoa(oldLineNum), lineNumWidth)
					output = append(output, fmt.Sprintf(" %s %s", lineNum, line))
					oldLineNum++
					newLineNum++
				}

				output = append(output, fmt.Sprintf(" %s ...", edPadLeft("", lineNumWidth)))
				oldLineNum += skippedLines
				newLineNum += skippedLines

				for _, line := range trailingLines {
					lineNum := edPadLeft(strconv.Itoa(oldLineNum), lineNumWidth)
					output = append(output, fmt.Sprintf(" %s %s", lineNum, line))
					oldLineNum++
					newLineNum++
				}
			}
		case hasLeadingChange:
			shownCount := ctxLines
			if len(raw) < shownCount {
				shownCount = len(raw)
			}
			shownLines := raw[:shownCount]
			skippedLines := len(raw) - len(shownLines)

			for _, line := range shownLines {
				lineNum := edPadLeft(strconv.Itoa(oldLineNum), lineNumWidth)
				output = append(output, fmt.Sprintf(" %s %s", lineNum, line))
				oldLineNum++
				newLineNum++
			}
			if skippedLines > 0 {
				output = append(output, fmt.Sprintf(" %s ...", edPadLeft("", lineNumWidth)))
				oldLineNum += skippedLines
				newLineNum += skippedLines
			}
		case hasTrailingChange:
			skippedLines := len(raw) - ctxLines
			if skippedLines < 0 {
				skippedLines = 0
			}
			if skippedLines > 0 {
				output = append(output, fmt.Sprintf(" %s ...", edPadLeft("", lineNumWidth)))
				oldLineNum += skippedLines
				newLineNum += skippedLines
			}
			for _, line := range raw[skippedLines:] {
				lineNum := edPadLeft(strconv.Itoa(oldLineNum), lineNumWidth)
				output = append(output, fmt.Sprintf(" %s %s", lineNum, line))
				oldLineNum++
				newLineNum++
			}
		default:
			oldLineNum += len(raw)
			newLineNum += len(raw)
		}

		lastWasChange = false
	}

	return DiffStringResult{
		Diff:             strings.Join(output, "\n"),
		FirstChangedLine: firstChangedLine,
	}
}
