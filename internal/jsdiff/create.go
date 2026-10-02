package jsdiff

import (
	"errors"
	"strconv"
	"strings"
)

// HeaderOptions selects the header lines FormatPatch writes.
type HeaderOptions struct {
	IncludeIndex       bool `json:"includeIndex"`
	IncludeUnderline   bool `json:"includeUnderline"`
	IncludeFileHeaders bool `json:"includeFileHeaders"`
}

var (
	// IncludeHeaders writes the Index line, the ==== underline and the ---/+++ file
	// headers (TS INCLUDE_HEADERS, the FormatPatch default).
	IncludeHeaders = HeaderOptions{IncludeIndex: true, IncludeUnderline: true, IncludeFileHeaders: true}
	// FileHeadersOnly writes only the ---/+++ file headers (TS FILE_HEADERS_ONLY).
	FileHeadersOnly = HeaderOptions{IncludeIndex: false, IncludeUnderline: false, IncludeFileHeaders: true}
	// OmitHeaders writes no header lines (TS OMIT_HEADERS).
	OmitHeaders = HeaderOptions{IncludeIndex: false, IncludeUnderline: false, IncludeFileHeaders: false}
)

// StructuredPatchHunk is one hunk of a StructuredPatch.
type StructuredPatchHunk struct {
	OldStart int      `json:"oldStart"`
	OldLines int      `json:"oldLines"`
	NewStart int      `json:"newStart"`
	NewLines int      `json:"newLines"`
	Lines    []string `json:"lines"`
}

// StructuredPatch is a patch for one file, as returned by NewStructuredPatch and
// ParsePatch.
type StructuredPatch struct {
	OldFileName string `json:"oldFileName"`
	NewFileName string `json:"newFileName"`
	// OldHeader and NewHeader are nil for undefined.
	OldHeader *string               `json:"oldHeader,omitzero"`
	NewHeader *string               `json:"newHeader,omitzero"`
	Hunks     []StructuredPatchHunk `json:"hunks"`
	// Index is the file name from an "Index:" or "diff" line (ParsePatch only).
	Index *string `json:"index,omitzero"`
}

// PatchOptions are the options of the patch-creation functions. Options holds the
// DiffLines options: TS passes the whole options object through to diffLines, so every
// line-diff option applies (NewlineIsToken is rejected).
type PatchOptions struct {
	Options
	// Context is the number of context lines around each change (default 4).
	Context *int
	// HeaderOptions is used by CreateTwoFilesPatch and CreatePatch (default
	// IncludeHeaders).
	HeaderOptions *HeaderOptions
}

// NewStructuredPatch is the TS structuredPatch function (renamed because the type
// StructuredPatch keeps the name): it diffs oldStr and newStr by lines and returns the
// hunks. oldHeader and newHeader are nil for undefined. It returns nil if MaxEditLength
// or Timeout was exceeded.
func NewStructuredPatch(oldFileName, newFileName, oldStr, newStr string, oldHeader, newHeader *string, options *PatchOptions) *StructuredPatch {
	var optionsObj PatchOptions
	if options != nil {
		optionsObj = *options
	}
	// TS writes the default back into the caller's options object; the Go port does not
	// mutate the caller's struct.
	context := 4
	if optionsObj.Context != nil {
		context = *optionsObj.Context
	}
	if optionsObj.NewlineIsToken {
		panic("newlineIsToken may not be used with patch-generation functions, only with diffing functions")
	}
	return diffLinesResultToPatch(DiffLines(oldStr, newStr, &optionsObj.Options), context, oldFileName, newFileName, oldHeader, newHeader)
}

type patchEntry struct {
	value   string
	added   bool
	removed bool
	lines   []string
}

func contextLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, entry := range lines {
		out[i] = " " + entry
	}
	return out
}

func diffLinesResultToPatch(diff []Change, context int, oldFileName, newFileName string, oldHeader, newHeader *string) *StructuredPatch {
	// STEP 1: Build up the patch with no "\ No newline at end of file" lines and with the
	//         arrays of lines containing trailing newline characters. We'll tidy up
	//         later...
	if diff == nil {
		return nil
	}
	entries := make([]patchEntry, 0, len(diff)+1)
	for _, c := range diff {
		entries = append(entries, patchEntry{value: c.Value, added: c.Added, removed: c.Removed})
	}
	entries = append(entries, patchEntry{value: "", lines: []string{}}) // Append an empty value to make cleanup easier

	hunks := []StructuredPatchHunk{}
	oldRangeStart, newRangeStart := 0, 0
	curRange := []string{}
	oldLine, newLine := 1, 1
	for i := range entries {
		current := &entries[i]
		lines := current.lines
		if lines == nil {
			lines = splitLines(current.value)
		}
		current.lines = lines
		if current.added || current.removed {
			// If we have previous context, start with that
			if oldRangeStart == 0 {
				oldRangeStart = oldLine
				newRangeStart = newLine
				if i > 0 {
					prev := entries[i-1]
					if context > 0 {
						curRange = contextLines(jsSlice(prev.lines, -context, len(prev.lines)))
					} else {
						curRange = []string{}
					}
					oldRangeStart -= len(curRange)
					newRangeStart -= len(curRange)
				}
			}
			// Output our changes
			sign := "-"
			if current.added {
				sign = "+"
			}
			for _, line := range lines {
				curRange = append(curRange, sign+line)
			}
			// Track the updated file position
			if current.added {
				newLine += len(lines)
			} else {
				oldLine += len(lines)
			}
		} else {
			// Identical context lines. Track line changes
			if oldRangeStart != 0 {
				// Close out any changes that have been output (or join overlapping)
				if len(lines) <= context*2 && i < len(entries)-2 {
					// Overlapping
					curRange = append(curRange, contextLines(lines)...)
				} else {
					// end the range and output
					contextSize := min(len(lines), context)
					curRange = append(curRange, contextLines(jsSlice(lines, 0, contextSize))...)
					hunks = append(hunks, StructuredPatchHunk{
						OldStart: oldRangeStart,
						OldLines: oldLine - oldRangeStart + contextSize,
						NewStart: newRangeStart,
						NewLines: newLine - newRangeStart + contextSize,
						Lines:    curRange,
					})
					oldRangeStart = 0
					newRangeStart = 0
					curRange = []string{}
				}
			}
			oldLine += len(lines)
			newLine += len(lines)
		}
	}

	// Step 2: eliminate the trailing `\n` from each line of each hunk, and, where needed,
	//         add "\ No newline at end of file".
	for h := range hunks {
		hunk := &hunks[h]
		out := make([]string, 0, len(hunk.Lines)+1)
		for _, line := range hunk.Lines {
			if strings.HasSuffix(line, "\n") {
				out = append(out, line[:len(line)-1])
			} else {
				out = append(out, line, `\ No newline at end of file`)
			}
		}
		hunk.Lines = out
	}
	return &StructuredPatch{
		OldFileName: oldFileName,
		NewFileName: newFileName,
		OldHeader:   oldHeader,
		NewHeader:   newHeader,
		Hunks:       hunks,
	}
}

// jsSlice is Array.prototype.slice(start, end) for in-range integer arguments that may
// be negative.
func jsSlice[T any](arr []T, start, end int) []T {
	n := len(arr)
	if start < 0 {
		start = max(n+start, 0)
	} else {
		start = min(start, n)
	}
	if end < 0 {
		end = max(n+end, 0)
	} else {
		end = min(end, n)
	}
	if start >= end {
		return []T{}
	}
	return arr[start:end]
}

// splitLines splits text into an array of lines, including the trailing newline
// character (where present).
func splitLines(text string) []string {
	hasTrailingNl := strings.HasSuffix(text, "\n")
	parts := strings.Split(text, "\n")
	result := make([]string, len(parts))
	for i, line := range parts {
		result[i] = line + "\n"
	}
	if hasTrailingNl {
		result = result[:len(result)-1]
	} else {
		last := result[len(result)-1]
		result[len(result)-1] = last[:len(last)-1]
	}
	return result
}

const multiFileHeaderError = "Cannot omit file headers on a multi-file patch. " +
	"(The result would be unparseable; how would a tool trying to apply " +
	"the patch know which changes are to which file?)"

// FormatPatch creates a unified diff patch from a structured patch. headerOptions nil
// means IncludeHeaders.
//
// TS parity: like formatPatch, it decrements OldStart/NewStart of hunks whose
// OldLines/NewLines is 0 in place (the unified diff format quirk), so formatting the
// same patch twice shifts those hunks again.
func FormatPatch(patch *StructuredPatch, headerOptions *HeaderOptions) string {
	if headerOptions == nil {
		headerOptions = &IncludeHeaders
	}
	ret := []string{}
	if headerOptions.IncludeIndex && patch.OldFileName == patch.NewFileName {
		ret = append(ret, "Index: "+patch.OldFileName)
	}
	if headerOptions.IncludeUnderline {
		ret = append(ret, "===================================================================")
	}
	if headerOptions.IncludeFileHeaders {
		ret = append(ret, "--- "+patch.OldFileName+headerSuffix(patch.OldHeader))
		ret = append(ret, "+++ "+patch.NewFileName+headerSuffix(patch.NewHeader))
	}
	for i := range patch.Hunks {
		hunk := &patch.Hunks[i]
		// Unified Diff Format quirk: If the chunk size is 0,
		// the first number is one lower than one would expect.
		// https://www.artima.com/weblogs/viewpost.jsp?thread=164293
		if hunk.OldLines == 0 {
			hunk.OldStart--
		}
		if hunk.NewLines == 0 {
			hunk.NewStart--
		}
		ret = append(ret, "@@ -"+strconv.Itoa(hunk.OldStart)+","+strconv.Itoa(hunk.OldLines)+
			" +"+strconv.Itoa(hunk.NewStart)+","+strconv.Itoa(hunk.NewLines)+" @@")
		ret = append(ret, hunk.Lines...)
	}
	return strings.Join(ret, "\n") + "\n"
}

func headerSuffix(header *string) string {
	if header == nil {
		return ""
	}
	return "\t" + *header
}

// FormatPatchList is FormatPatch for an array of structured patches (as returned by
// ParsePatch): the formatted patches joined with "\n". Omitting file headers on a
// multi-file patch is an error.
func FormatPatchList(patches []*StructuredPatch, headerOptions *HeaderOptions) (string, error) {
	if headerOptions == nil {
		headerOptions = &IncludeHeaders
	}
	if len(patches) > 1 && !headerOptions.IncludeFileHeaders {
		return "", errors.New(multiFileHeaderError)
	}
	parts := make([]string, len(patches))
	for i, p := range patches {
		parts[i] = FormatPatch(p, headerOptions)
	}
	return strings.Join(parts, "\n"), nil
}

// CreateTwoFilesPatch creates a unified diff patch comparing oldStr (oldFileName) with
// newStr (newFileName). oldHeader and newHeader are nil for undefined. It returns "" if
// MaxEditLength or Timeout was exceeded (TS: undefined); a real patch always ends with
// "\n".
func CreateTwoFilesPatch(oldFileName, newFileName, oldStr, newStr string, oldHeader, newHeader *string, options *PatchOptions) string {
	patchObj := NewStructuredPatch(oldFileName, newFileName, oldStr, newStr, oldHeader, newHeader, options)
	if patchObj == nil {
		return ""
	}
	var headerOptions *HeaderOptions
	if options != nil {
		headerOptions = options.HeaderOptions
	}
	return FormatPatch(patchObj, headerOptions)
}

// CreatePatch is CreateTwoFilesPatch with the same file name on both sides.
func CreatePatch(fileName, oldStr, newStr string, oldHeader, newHeader *string, options *PatchOptions) string {
	return CreateTwoFilesPatch(fileName, fileName, oldStr, newStr, oldHeader, newHeader, options)
}
