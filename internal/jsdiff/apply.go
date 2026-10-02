package jsdiff

import (
	"errors"
	"strings"
)

// ApplyPatchOptions are the options of ApplyPatch.
type ApplyPatchOptions struct {
	// FuzzFactor is the maximum number of mismatches (missing, extra, or changed context
	// lines) allowed when fitting a hunk. Must be non-negative.
	FuzzFactor int
	// AutoConvertLineEndings converts the patch between Unix and Windows line endings to
	// match source (nil means true).
	AutoConvertLineEndings *bool
	// CompareLine decides whether a line of the source (nil when the position is outside
	// the source, TS undefined) matches the patch content. lineNumber is 1-based.
	CompareLine func(lineNumber int, line *string, operation string, patchContent string) bool
}

// ApplyPatch attempts to apply a unified diff patch (a patch string, parsed with
// ParsePatch) to source.
//
// Hunks are applied first to last. ApplyPatch first tries to apply the first hunk at the
// line number specified in the hunk header, and with all context lines matching exactly.
// If that fails, it tries scanning backwards and forwards, one line at a time, to find a
// place to apply the hunk where the context lines match exactly. If that still fails,
// and FuzzFactor is greater than zero, it increments the maximum number of mismatches
// (missing, extra, or changed context lines) that there can be between the hunk context
// and a region where we are trying to apply the patch such that the hunk will still be
// considered to match. Regardless of FuzzFactor, lines to be deleted in the hunk *must*
// be present for a hunk to match, and the context lines *immediately* before and after an
// insertion must match exactly.
//
// Once a hunk is successfully fitted, the process begins again with the next hunk.
// Regardless of FuzzFactor, later hunks must be applied later in the file than earlier
// hunks.
//
// If a hunk cannot be successfully fitted *anywhere* with fewer than FuzzFactor
// mismatches, ApplyPatch fails and returns ok=false (TS: false).
//
// If a hunk is successfully fitted but not at the line number specified by the hunk
// header, all subsequent hunks have their target line number adjusted accordingly.
//
// err reports what TS throws: a malformed patch, a patch for more than one file, or an
// invalid FuzzFactor.
func ApplyPatch(source, uniDiff string, options *ApplyPatchOptions) (result string, ok bool, err error) {
	patches, err := ParsePatch(uniDiff)
	if err != nil {
		return "", false, err
	}
	return ApplyPatchList(source, patches, options)
}

// ApplyPatchStructured is ApplyPatch for a structured patch (from NewStructuredPatch or
// ParsePatch).
func ApplyPatchStructured(source string, patch *StructuredPatch, options *ApplyPatchOptions) (result string, ok bool, err error) {
	return ApplyPatchList(source, []*StructuredPatch{patch}, options)
}

// ApplyPatchList is ApplyPatch for an array of structured patches, which must contain
// exactly one patch.
func ApplyPatchList(source string, patches []*StructuredPatch, options *ApplyPatchOptions) (result string, ok bool, err error) {
	if len(patches) > 1 {
		return "", false, errors.New("applyPatch only works with a single input.")
	}
	if len(patches) == 0 || patches[0] == nil {
		return "", false, errors.New("Cannot read properties of undefined (reading 'hunks')")
	}
	if options == nil {
		options = &ApplyPatchOptions{}
	}
	return applyStructuredPatch(source, patches[0], options)
}

func applyStructuredPatch(source string, patch *StructuredPatch, options *ApplyPatchOptions) (string, bool, error) {
	if options.AutoConvertLineEndings == nil || *options.AutoConvertLineEndings {
		if hasOnlyWinLineEndings(source) && isUnix(patch) {
			patch = unixToWin(patch)
		} else if hasOnlyUnixLineEndings(source) && isWin(patch) {
			patch = winToUnix(patch)
		}
	}

	// Apply the diff to the input
	lines := strings.Split(source, "\n")
	hunks := patch.Hunks
	compareLine := options.CompareLine
	if compareLine == nil {
		compareLine = func(_ int, line *string, _ string, patchContent string) bool {
			return line != nil && *line == patchContent
		}
	}
	fuzzFactor := options.FuzzFactor
	minLine := 0
	if fuzzFactor < 0 {
		return "", false, errors.New("fuzzFactor must be a non-negative integer")
	}

	// Special case for empty patch.
	if len(hunks) == 0 {
		return source, true, nil
	}

	// Before anything else, handle EOFNL insertion/removal. If the patch tells us to make
	// a change to the EOFNL that is redundant/impossible - i.e. to remove a newline that's
	// not there, or add a newline that already exists - then we either return false and
	// fail to apply the patch (if fuzzFactor is 0) or simply ignore the problem and do
	// nothing (if fuzzFactor is >0). If we do need to remove/add a newline at EOF, this
	// will always be in the final hunk:
	prevLine := ""
	removeEOFNL, addEOFNL := false, false
	for _, line := range hunks[len(hunks)-1].Lines {
		if strings.HasPrefix(line, `\`) {
			if strings.HasPrefix(prevLine, "+") {
				removeEOFNL = true
			} else if strings.HasPrefix(prevLine, "-") {
				addEOFNL = true
			}
		}
		prevLine = line
	}
	if removeEOFNL {
		if addEOFNL {
			// This means the final line gets changed but doesn't have a trailing newline
			// in either the original or patched version. In that case, we do nothing if
			// fuzzFactor > 0, and if fuzzFactor is 0, we simply validate that the source
			// file has no trailing newline.
			if fuzzFactor == 0 && lines[len(lines)-1] == "" {
				return "", false, nil
			}
		} else if lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		} else if fuzzFactor == 0 {
			return "", false, nil
		}
	} else if addEOFNL {
		if len(lines) == 0 || lines[len(lines)-1] != "" {
			lines = append(lines, "")
		} else if fuzzFactor == 0 {
			return "", false, nil
		}
	}

	lineAt := func(pos int) *string {
		if pos >= 0 && pos < len(lines) {
			return &lines[pos]
		}
		return nil
	}

	// patched is the patchedLines array shared by the recursive applyHunk calls. A nil
	// entry is an undefined element (joined as "").
	type hunkResult struct {
		patchedLines []*string
		oldLineLastI int
	}
	var patched []*string
	setPatched := func(i int, v *string) {
		for len(patched) <= i {
			patched = append(patched, nil)
		}
		patched[i] = v
	}

	// applyHunk checks if the hunk can be made to fit at the provided location with at
	// most maxErrors insertions, substitutions, or deletions, while ensuring also that:
	//   - lines deleted in the hunk match exactly, and
	//   - wherever an insertion operation or block of insertion operations appears in the
	//     hunk, the immediately preceding and following lines of context match exactly
	//
	// toPos should be set such that lines[toPos] is meant to match hunkLines[0].
	//
	// If the hunk can be applied, returns the replacement lines and oldLineLastI.
	// Otherwise, returns nil.
	var applyHunk func(hunkLines []string, toPos, maxErrors, hunkLinesI int, lastContextLineMatched bool, patchedLinesLength int) *hunkResult
	applyHunk = func(hunkLines []string, toPos, maxErrors, hunkLinesI int, lastContextLineMatched bool, patchedLinesLength int) *hunkResult {
		nConsecutiveOldContextLines := 0
		nextContextLineMustMatch := false
		for ; hunkLinesI < len(hunkLines); hunkLinesI++ {
			hunkLine := hunkLines[hunkLinesI]
			operation, content := " ", hunkLine
			if len(hunkLine) > 0 {
				// hunkLine[0] / hunkLine.substr(1): the operation characters are ASCII, so a
				// non-ASCII first character is never one of them.
				operation = hunkLine[:1]
				content = hunkLine[1:]
			}
			if operation == "-" {
				if compareLine(toPos+1, lineAt(toPos), operation, content) {
					toPos++
					nConsecutiveOldContextLines = 0
				} else {
					if maxErrors == 0 || lineAt(toPos) == nil {
						return nil
					}
					setPatched(patchedLinesLength, lineAt(toPos))
					return applyHunk(hunkLines, toPos+1, maxErrors-1, hunkLinesI, false, patchedLinesLength+1)
				}
			}
			if operation == "+" {
				if !lastContextLineMatched {
					return nil
				}
				c := content
				setPatched(patchedLinesLength, &c)
				patchedLinesLength++
				nConsecutiveOldContextLines = 0
				nextContextLineMustMatch = true
			}
			if operation == " " {
				nConsecutiveOldContextLines++
				setPatched(patchedLinesLength, lineAt(toPos))
				if compareLine(toPos+1, lineAt(toPos), operation, content) {
					patchedLinesLength++
					lastContextLineMatched = true
					nextContextLineMustMatch = false
					toPos++
				} else {
					if nextContextLineMustMatch || maxErrors == 0 {
						return nil
					}
					// Consider 3 possibilities in sequence:
					// 1. lines contains a *substitution* not included in the patch context, or
					// 2. lines contains an *insertion* not included in the patch context, or
					// 3. lines contains a *deletion* not included in the patch context
					// The first two options are of course only possible if the line from lines
					// is non-null - i.e. only option 3 is possible if we've overrun the end of
					// the old file.
					if l := lineAt(toPos); l != nil && *l != "" {
						if r := applyHunk(hunkLines, toPos+1, maxErrors-1, hunkLinesI+1, false, patchedLinesLength+1); r != nil {
							return r
						}
						if r := applyHunk(hunkLines, toPos+1, maxErrors-1, hunkLinesI, false, patchedLinesLength+1); r != nil {
							return r
						}
					}
					return applyHunk(hunkLines, toPos, maxErrors-1, hunkLinesI+1, false, patchedLinesLength)
				}
			}
		}
		// Before returning, trim any unmodified context lines off the end of patchedLines
		// and reduce toPos (and thus oldLineLastI) accordingly. This allows later hunks to
		// be applied to a region that starts in this hunk's trailing context.
		patchedLinesLength -= nConsecutiveOldContextLines
		toPos -= nConsecutiveOldContextLines
		for len(patched) < patchedLinesLength {
			patched = append(patched, nil)
		}
		patched = patched[:patchedLinesLength]
		return &hunkResult{patchedLines: patched, oldLineLastI: toPos - 1}
	}

	resultLines := []string{}
	appendLine := func(l *string) {
		if l == nil {
			resultLines = append(resultLines, "")
		} else {
			resultLines = append(resultLines, *l)
		}
	}

	// Search best fit offsets for each hunk based on the previous ones
	prevHunkOffset := 0
	for i := range hunks {
		hunk := &hunks[i]
		var result *hunkResult
		maxLine := len(lines) - hunk.OldLines + fuzzFactor
		var toPos int
		for maxErrors := 0; maxErrors <= fuzzFactor; maxErrors++ {
			toPos = hunk.OldStart + prevHunkOffset - 1
			iterator := distanceIterator(toPos, minLine, maxLine)
			for hasPos := true; hasPos; toPos, hasPos = iterator() {
				patched = []*string{}
				result = applyHunk(hunk.Lines, toPos, maxErrors, 0, true, 0)
				if result != nil {
					break
				}
			}
			if result != nil {
				break
			}
		}
		if result == nil {
			return "", false, nil
		}

		// Copy everything from the end of where we applied the last hunk to the start of
		// this hunk
		for k := minLine; k < toPos; k++ {
			appendLine(lineAt(k))
		}
		// Add the lines produced by applying the hunk:
		for _, line := range result.patchedLines {
			appendLine(line)
		}
		// Set lower text limit to end of the current hunk, so next ones don't try to fit
		// over already patched text
		minLine = result.oldLineLastI + 1
		// Note the offset between where the patch said the hunk should've applied and
		// where we applied it, so we can adjust future hunks accordingly:
		prevHunkOffset = toPos + 1 - hunk.OldStart
	}

	// Copy over the rest of the lines from the old text
	for k := minLine; k < len(lines); k++ {
		appendLine(lineAt(k))
	}
	return strings.Join(resultLines, "\n"), true, nil
}

// distanceIterator traverses the range [minLine, maxLine], stepping by distance from a
// given start position. I.e. for [0, 4], with start of 2, this will iterate 2, 3, 1, 4,
// 0 (the first value, start itself, is the caller's). ok is false when exhausted.
func distanceIterator(start, minLine, maxLine int) func() (int, bool) {
	wantForward, backwardExhausted, forwardExhausted := true, false, false
	localOffset := 1
	var iterator func() (int, bool)
	iterator = func() (int, bool) {
		if wantForward && !forwardExhausted {
			if backwardExhausted {
				localOffset++
			} else {
				wantForward = false
			}
			// Check if trying to fit beyond text length, and if not, check it fits after
			// offset location (or desired location on first iteration)
			if start+localOffset <= maxLine {
				return start + localOffset, true
			}
			forwardExhausted = true
		}
		if !backwardExhausted {
			if !forwardExhausted {
				wantForward = true
			}
			// Check if trying to fit before text beginning, and if not, check it fits
			// before offset location
			if minLine <= start-localOffset {
				v := start - localOffset
				localOffset++
				return v, true
			}
			backwardExhausted = true
			return iterator()
		}
		// We tried to fit hunk before text beginning and beyond text length, then hunk
		// can't fit on the text.
		return 0, false
	}
	return iterator
}

// ApplyPatchesOptions are the callbacks of ApplyPatches.
type ApplyPatchesOptions struct {
	ApplyPatchOptions
	// LoadFile is called for each patch; the callback receives the file contents or an
	// error that terminates further patch execution.
	LoadFile func(index *StructuredPatch, callback func(err error, data string))
	// Patched is called with the result of applying the patch (ok=false: TS false); the
	// callback's error terminates further patch execution.
	Patched func(index *StructuredPatch, content string, ok bool, callback func(err error))
	// Complete is called once all patches have been applied or an error occurs.
	Complete func(err error)
}

// ApplyPatches applies one or more patches. uniDiff may patch one or more files. For each
// patch, LoadFile is called, the patch is applied to the loaded contents, and Patched is
// called; Complete is called at the end or on the first error. A malformed uniDiff is
// returned as an error (TS: thrown synchronously); an error from applying a patch is
// passed to Complete (TS: thrown from inside the LoadFile callback).
func ApplyPatches(uniDiff string, options *ApplyPatchesOptions) error {
	spDiff, err := ParsePatch(uniDiff)
	if err != nil {
		return err
	}
	ApplyPatchesList(spDiff, options)
	return nil
}

// ApplyPatchesList is ApplyPatches for already parsed patches.
func ApplyPatchesList(spDiff []*StructuredPatch, options *ApplyPatchesOptions) {
	currentIndex := 0
	var processIndex func()
	processIndex = func() {
		if currentIndex >= len(spDiff) || spDiff[currentIndex] == nil {
			currentIndex++
			options.Complete(nil)
			return
		}
		index := spDiff[currentIndex]
		currentIndex++
		options.LoadFile(index, func(err error, data string) {
			if err != nil {
				options.Complete(err)
				return
			}
			applyOpts := options.ApplyPatchOptions
			updatedContent, ok, applyErr := ApplyPatchStructured(data, index, &applyOpts)
			if applyErr != nil {
				options.Complete(applyErr)
				return
			}
			options.Patched(index, updatedContent, ok, func(err error) {
				if err != nil {
					options.Complete(err)
					return
				}
				processIndex()
			})
		})
	}
	processIndex()
}
