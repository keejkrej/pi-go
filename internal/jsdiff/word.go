package jsdiff

import "strings"

// isExtendedWordChar reports whether r is in extendedWordChars, based on
// https://en.wikipedia.org/wiki/Latin_script_in_Unicode:
//
//	a-z A-Z 0-9 _
//	U+00AD        Soft hyphen
//	U+00C0-U+00FF letters with diacritics from the Latin-1 Supplement, except
//	              U+00D7 × Multiplication sign and U+00F7 ÷ Division sign
//	U+0100-U+017F Latin Extended-A
//	U+0180-U+024F Latin Extended-B
//	U+0250-U+02AF IPA Extensions
//	U+02B0-U+02FF Spacing Modifier Letters, except U+02C7 Caron, U+02D8 Breve,
//	              U+02D9 Dot Above, U+02DA Ring Above, U+02DB Ogonek,
//	              U+02DC Small Tilde, U+02DD Double Acute Accent
//	U+1E00-U+1EFF Latin Extended Additional
func isExtendedWordChar(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
		return true
	case r == 0xAD,
		r >= 0xC0 && r <= 0xD6,
		r >= 0xD8 && r <= 0xF6,
		r >= 0xF8 && r <= 0x2C6,
		r >= 0x2C8 && r <= 0x2D7,
		r >= 0x2DE && r <= 0x2FF,
		r >= 0x1E00 && r <= 0x1EFF:
		return true
	}
	return false
}

// scanRun returns the end of the run of characters starting at i that satisfy pred.
func scanRun(value string, i int, pred func(rune) bool) int {
	for i < len(value) {
		r, size := decodeChar(value, i)
		if !pred(r) {
			break
		}
		i += size
	}
	return i
}

// tokenizeIncludingWhitespace is value.match(/[W]+|\s+|[^W]/ug) where W is
// extendedWordChars. It gives runs of whitespace their own "token"; WordDiff's tokenize
// then stitches whitespace tokens onto adjacent word or punctuation tokens.
func tokenizeIncludingWhitespace(value string) []string {
	parts := []string{}
	for i := 0; i < len(value); {
		r, size := decodeChar(value, i)
		start := i
		switch {
		case isExtendedWordChar(r):
			i = scanRun(value, i, isExtendedWordChar)
		case isJSSpace(r):
			i = scanRun(value, i, isJSSpace)
		default:
			i += size
		}
		parts = append(parts, value[start:i])
	}
	return parts
}

// Each token is one of the following:
//   - A punctuation mark plus the surrounding whitespace
//   - A word plus the surrounding whitespace
//   - Pure whitespace (but only in the special case where the entire text is just
//     whitespace)
//
// We have to include surrounding whitespace in the tokens because the two alternative
// approaches produce horribly broken results:
//   - If we just discard the whitespace, we can't fully reproduce the original text from
//     the sequence of tokens and any attempt to render the diff will get the whitespace
//     wrong.
//   - If we have separate tokens for whitespace, then in a typical text every second
//     token will be a single space character. But this often results in the optimal diff
//     between two texts being a perverse one that preserves the spaces between words but
//     deletes and reinserts actual common words. See
//     https://github.com/kpdecker/jsdiff/issues/160#issuecomment-1866099640 for an
//     example.
//
// Keeping the surrounding whitespace of course has implications for equals and join,
// not just tokenize.
func wordTokenize(value string, options *Options) []string {
	var parts []string
	if options.IntlSegmenter != nil {
		segmenter := options.IntlSegmenter
		if segmenter.Granularity() != "word" {
			panic(segmenterGranularityError)
		}
		// We want `parts` to be an array whose elements alternate between being pure
		// whitespace and being pure non-whitespace. This is ALMOST what the segments
		// returned by a word-based Intl.Segmenter already look like, but not quite - see
		// explanation in the docs of segment().
		parts = segment(value, segmenter)
	} else {
		parts = tokenizeIncludingWhitespace(value)
	}
	tokens := make([]string, 0, len(parts))
	hasPrev := false
	prevPart := ""
	for _, part := range parts {
		if containsJSSpace(part) {
			if !hasPrev {
				tokens = append(tokens, part)
			} else {
				tokens[len(tokens)-1] += part
			}
		} else if hasPrev && containsJSSpace(prevPart) {
			if tokens[len(tokens)-1] == prevPart {
				tokens[len(tokens)-1] += part
			} else {
				tokens = append(tokens, prevPart+part)
			}
		} else {
			tokens = append(tokens, part)
		}
		prevPart = part
		hasPrev = true
	}
	return tokens
}

func wordEquals(left, right string, options *Options) bool {
	if options.IgnoreCase {
		left = jsToLower(left)
		right = jsToLower(right)
	}
	return jsTrim(left) == jsTrim(right)
}

// wordJoin joins tokens that always appeared consecutively in the same text, so it can
// simply strip off the leading whitespace from all the tokens except the first (and
// except any whitespace-only tokens - but such a token will always be the first and only
// token anyway) and then join them, and the whitespace around words and punctuation will
// end up correct.
func wordJoin(tokens []string) string {
	var b strings.Builder
	for i, token := range tokens {
		if i == 0 {
			b.WriteString(token)
		} else {
			b.WriteString(token[leadingSpaceLen(token):])
		}
	}
	return b.String()
}

func wordPostProcess(changes []Change, options *Options) []Change {
	if changes == nil || options.OneChangePerToken {
		return changes
	}
	var lastKeep *Change
	// Change objects representing any insertion or deletion since the last "keep"
	// change object. There can be at most one of each.
	var insertion, deletion *Change
	for i := range changes {
		change := &changes[i]
		if change.Added {
			insertion = change
		} else if change.Removed {
			deletion = change
		} else {
			if insertion != nil || deletion != nil { // May be false at start of text
				dedupeWhitespaceInChangeObjects(lastKeep, deletion, insertion, change, options.IntlSegmenter)
			}
			lastKeep = change
			insertion = nil
			deletion = nil
		}
	}
	if insertion != nil || deletion != nil {
		dedupeWhitespaceInChangeObjects(lastKeep, deletion, insertion, nil, options.IntlSegmenter)
	}
	return changes
}

// WordDiff is the jsdiff wordDiff instance (class WordDiff).
var WordDiff = &Diff{
	Tokenize:    wordTokenize,
	Equals:      wordEquals,
	Join:        wordJoin,
	PostProcess: wordPostProcess,
}

// DiffWords diffs two blocks of text, treating each word and each punctuation mark as a
// token. Whitespace is ignored when computing the diff (but preserved as far as possible
// in the final change objects). It returns nil if MaxEditLength or Timeout was exceeded.
func DiffWords(oldStr, newStr string, options *Options) []Change {
	// This option has never been documented and never will be (it's clearer to just
	// call DiffWordsWithSpace directly if you need that behavior), but has existed in
	// jsdiff for a long time, so we retain support for it here for the sake of backwards
	// compatibility.
	if options != nil && options.IgnoreWhitespace != nil && !*options.IgnoreWhitespace {
		return DiffWordsWithSpace(oldStr, newStr, options)
	}
	return WordDiff.Diff(oldStr, newStr, options)
}

func dedupeWhitespaceInChangeObjects(startKeep, deletion, insertion, endKeep *Change, segmenter Segmenter) {
	// Before returning, we tidy up the leading and trailing whitespace of the change
	// objects to eliminate cases where trailing whitespace in one object is repeated as
	// leading whitespace in the next.
	// Below are examples of the outcomes we want here to explain the code.
	// I=insert, K=keep, D=delete
	// 1. diffing 'foo bar baz' vs 'foo baz'
	//    Prior to cleanup, we have K:'foo ' D:' bar ' K:' baz'
	//    After cleanup, we want:   K:'foo ' D:'bar ' K:'baz'
	//
	// 2. Diffing 'foo bar baz' vs 'foo qux baz'
	//    Prior to cleanup, we have K:'foo ' D:' bar ' I:' qux ' K:' baz'
	//    After cleanup, we want K:'foo ' D:'bar' I:'qux' K:' baz'
	//
	// 3. Diffing 'foo\nbar baz' vs 'foo baz'
	//    Prior to cleanup, we have K:'foo ' D:'\nbar ' K:' baz'
	//    After cleanup, we want K'foo' D:'\nbar' K:' baz'
	//
	// 4. Diffing 'foo baz' vs 'foo\nbar baz'
	//    Prior to cleanup, we have K:'foo\n' I:'\nbar ' K:' baz'
	//    After cleanup, we ideally want K'foo' I:'\nbar' K:' baz'
	//    but don't actually manage this currently (the pre-cleanup change
	//    objects don't contain enough information to make it possible).
	//
	// 5. Diffing 'foo   bar baz' vs 'foo  baz'
	//    Prior to cleanup, we have K:'foo  ' D:'   bar ' K:'  baz'
	//    After cleanup, we want K:'foo  ' D:' bar ' K:'baz'
	//
	// Our handling is unavoidably imperfect in the case where there's a single indel
	// between keeps and the whitespace has changed. For instance, consider diffing
	// 'foo\tbar\nbaz' vs 'foo baz'. Unless we create an extra change object to represent
	// the insertion of the space character (which isn't even a token), we have no way to
	// avoid losing information about the texts' original whitespace in the result we
	// return. Still, we do our best to output something that will look sensible if we
	// e.g. print it with insertions in green and deletions in red.
	//
	// Between two "keep" change objects (or before the first or after the last change
	// object), we can have either:
	// * A "delete" followed by an "insert"
	// * Just an "insert"
	// * Just a "delete"
	// We handle the three cases separately.
	if deletion != nil && insertion != nil {
		oldWsPrefix, oldWsSuffix := leadingAndTrailingWs(deletion.Value, segmenter)
		newWsPrefix, newWsSuffix := leadingAndTrailingWs(insertion.Value, segmenter)
		if startKeep != nil {
			commonWsPrefix := longestCommonPrefix(oldWsPrefix, newWsPrefix)
			startKeep.Value = replaceSuffix(startKeep.Value, newWsPrefix, commonWsPrefix)
			deletion.Value = removePrefix(deletion.Value, commonWsPrefix)
			insertion.Value = removePrefix(insertion.Value, commonWsPrefix)
		}
		if endKeep != nil {
			commonWsSuffix := longestCommonSuffix(oldWsSuffix, newWsSuffix)
			endKeep.Value = replacePrefix(endKeep.Value, newWsSuffix, commonWsSuffix)
			deletion.Value = removeSuffix(deletion.Value, commonWsSuffix)
			insertion.Value = removeSuffix(insertion.Value, commonWsSuffix)
		}
	} else if insertion != nil {
		// The whitespaces all reflect what was in the new text rather than the old, so we
		// essentially have no information about whitespace insertion or deletion. We
		// just want to dedupe the whitespace. We do that by having each change object
		// keep its trailing whitespace and deleting duplicate leading whitespace where
		// present.
		if startKeep != nil {
			ws := leadingWs(insertion.Value, segmenter)
			insertion.Value = insertion.Value[len(ws):]
		}
		if endKeep != nil {
			ws := leadingWs(endKeep.Value, segmenter)
			endKeep.Value = endKeep.Value[len(ws):]
		}
		// otherwise we've got a deletion and no insertion
	} else if startKeep != nil && endKeep != nil {
		newWsFull := leadingWs(endKeep.Value, segmenter)
		delWsStart, delWsEnd := leadingAndTrailingWs(deletion.Value, segmenter)
		// Any whitespace that comes straight after startKeep in both the old and new
		// texts, assign to startKeep and remove from the deletion.
		newWsStart := longestCommonPrefix(newWsFull, delWsStart)
		deletion.Value = removePrefix(deletion.Value, newWsStart)
		// Any whitespace that comes straight before endKeep in both the old and new
		// texts, and hasn't already been assigned to startKeep, assign to endKeep and
		// remove from the deletion.
		newWsEnd := longestCommonSuffix(removePrefix(newWsFull, newWsStart), delWsEnd)
		deletion.Value = removeSuffix(deletion.Value, newWsEnd)
		endKeep.Value = replacePrefix(endKeep.Value, newWsFull, newWsEnd)
		// If there's any whitespace from the new text that HASN'T already been assigned,
		// assign it to the start:
		fullUnits := toUTF16(newWsFull)
		startKeep.Value = replaceSuffix(startKeep.Value, newWsFull, fromUTF16(fullUnits[:len(fullUnits)-utf16Len(newWsEnd)]))
	} else if endKeep != nil {
		// We are at the start of the text. Preserve all the whitespace on endKeep, and
		// just remove whitespace from the end of deletion to the extent that it overlaps
		// with the start of endKeep.
		endKeepWsPrefix := leadingWs(endKeep.Value, segmenter)
		deletionWsSuffix := trailingWs(deletion.Value, segmenter)
		overlap := maximumOverlap(deletionWsSuffix, endKeepWsPrefix)
		deletion.Value = removeSuffix(deletion.Value, overlap)
	} else if startKeep != nil {
		// We are at the END of the text. Preserve all the whitespace on startKeep, and
		// just remove whitespace from the start of deletion to the extent that it
		// overlaps with the end of startKeep.
		startKeepWsSuffix := trailingWs(startKeep.Value, segmenter)
		deletionWsPrefix := leadingWs(deletion.Value, segmenter)
		overlap := maximumOverlap(startKeepWsSuffix, deletionWsPrefix)
		deletion.Value = removePrefix(deletion.Value, overlap)
	}
}

// wordsWithSpaceTokenize is value.match(/(\r?\n)|[W]+|[^\S\n\r]+|[^W]/ug). Slightly
// different to tokenizeIncludingWhitespace in that it treats each individual newline as
// a distinct token, rather than merging them into other surrounding whitespace. This was
// requested in https://github.com/kpdecker/jsdiff/issues/180 &
// https://github.com/kpdecker/jsdiff/issues/211
func wordsWithSpaceTokenize(value string, _ *Options) []string {
	tokens := []string{}
	isInlineSpace := func(r rune) bool { return isJSSpace(r) && r != '\n' && r != '\r' }
	for i := 0; i < len(value); {
		r, size := decodeChar(value, i)
		start := i
		switch {
		case r == '\n':
			i++
		case r == '\r' && i+1 < len(value) && value[i+1] == '\n':
			i += 2
		case isExtendedWordChar(r):
			i = scanRun(value, i, isExtendedWordChar)
		case isInlineSpace(r):
			i = scanRun(value, i, isInlineSpace)
		default:
			i += size
		}
		tokens = append(tokens, value[start:i])
	}
	return tokens
}

// WordsWithSpaceDiff is the jsdiff wordsWithSpaceDiff instance (class
// WordsWithSpaceDiff).
var WordsWithSpaceDiff = &Diff{
	Tokenize: wordsWithSpaceTokenize,
}

// DiffWordsWithSpace diffs two blocks of text, treating each word, punctuation mark,
// newline, or run of (non-newline) whitespace as a token. It returns nil if
// MaxEditLength or Timeout was exceeded.
func DiffWordsWithSpace(oldStr, newStr string, options *Options) []Change {
	return WordsWithSpaceDiff.Diff(oldStr, newStr, options)
}
