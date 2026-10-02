package jsdiff

import (
	"strings"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

// Ported from util/string.js. The prefix/suffix helpers compare UTF-16 code units like
// the JS original.

func longestCommonPrefix(str1, str2 string) string {
	u1, u2 := toUTF16(str1), toUTF16(str2)
	i := 0
	for ; i < len(u1) && i < len(u2); i++ {
		if u1[i] != u2[i] {
			return fromUTF16(u1[:i])
		}
	}
	return fromUTF16(u1[:i])
}

func longestCommonSuffix(str1, str2 string) string {
	// Unlike longestCommonPrefix, we need a special case to handle all scenarios
	// where we return the empty string since str1.slice(-0) will return the
	// entire string.
	u1, u2 := toUTF16(str1), toUTF16(str2)
	if len(u1) == 0 || len(u2) == 0 || u1[len(u1)-1] != u2[len(u2)-1] {
		return ""
	}
	i := 0
	for ; i < len(u1) && i < len(u2); i++ {
		if u1[len(u1)-(i+1)] != u2[len(u2)-(i+1)] {
			return fromUTF16(u1[len(u1)-i:])
		}
	}
	return fromUTF16(u1[len(u1)-i:])
}

func jsonString(s string) string {
	out, err := jsonx.Stringify(s)
	if err != nil {
		return `"` + s + `"`
	}
	return out
}

func replacePrefix(str, oldPrefix, newPrefix string) string {
	if !strings.HasPrefix(str, oldPrefix) {
		panic("string " + jsonString(str) + " doesn't start with prefix " + jsonString(oldPrefix) + "; this is a bug")
	}
	return newPrefix + str[len(oldPrefix):]
}

func replaceSuffix(str, oldSuffix, newSuffix string) string {
	if oldSuffix == "" {
		return str + newSuffix
	}
	if !strings.HasSuffix(str, oldSuffix) {
		panic("string " + jsonString(str) + " doesn't end with suffix " + jsonString(oldSuffix) + "; this is a bug")
	}
	return str[:len(str)-len(oldSuffix)] + newSuffix
}

func removePrefix(str, oldPrefix string) string {
	return replacePrefix(str, oldPrefix, "")
}

func removeSuffix(str, oldSuffix string) string {
	return replaceSuffix(str, oldSuffix, "")
}

func maximumOverlap(string1, string2 string) string {
	u2 := toUTF16(string2)
	return fromUTF16(u2[:overlapCount(toUTF16(string1), u2)])
}

// overlapCount is nicked from https://stackoverflow.com/a/60422853/1709587 (as in jsdiff).
func overlapCount(a, b []uint16) int {
	// Deal with cases where the strings differ in length
	startA := 0
	if len(a) > len(b) {
		startA = len(a) - len(b)
	}
	endB := len(b)
	if len(a) < len(b) {
		endB = len(a)
	}
	// Create a back-reference for each index
	//   that should be followed in case of a mismatch.
	//   We only need B to make these references:
	backRef := make([]int, max(endB, 1))
	k := 0 // Index that lags behind j
	backRef[0] = 0
	for j := 1; j < endB; j++ {
		if b[j] == b[k] {
			backRef[j] = backRef[k] // skip over the same character (optional optimisation)
		} else {
			backRef[j] = k
		}
		for k > 0 && b[j] != b[k] {
			k = backRef[k]
		}
		if b[j] == b[k] {
			k++
		}
	}
	// Phase 2: use these references while iterating over A
	k = 0
	for i := startA; i < len(a); i++ {
		// k only reaches endB on the last character of a, so b[k] and backRef[k] stay
		// in range; the bounds checks just keep the port total.
		for k > 0 && (k >= len(b) || a[i] != b[k]) {
			if k >= len(backRef) {
				k = 0
				break
			}
			k = backRef[k]
		}
		if k < len(b) && a[i] == b[k] {
			k++
		}
	}
	return k
}

// hasOnlyWinLineEndings returns true if the string consistently uses Windows line endings.
func hasOnlyWinLineEndings(s string) bool {
	if !strings.Contains(s, "\r\n") || strings.HasPrefix(s, "\n") {
		return false
	}
	// !string.match(/[^\r]\n/)
	for i := 1; i < len(s); i++ {
		if s[i] == '\n' && s[i-1] != '\r' {
			return false
		}
	}
	return true
}

// hasOnlyUnixLineEndings returns true if the string consistently uses Unix line endings.
func hasOnlyUnixLineEndings(s string) bool {
	return !strings.Contains(s, "\r\n") && strings.Contains(s, "\n")
}

// Segmenter stands in for the Intl.Segmenter that the TS intlSegmenter option accepts.
type Segmenter interface {
	// Granularity is resolvedOptions().granularity; diffWords requires "word".
	Granularity() string
	// Segment returns the `segment` strings of segmenter.segment(s), in order.
	Segment(s string) []string
}

const segmenterGranularityError = `The segmenter passed must have a granularity of "word"`

// segment splits a string into segments using a word segmenter, merging consecutive
// segments if they are both whitespace segments. Whitespace segments can appear adjacent
// to one another for two reasons:
//   - newlines always get their own segment
//   - where a diacritic is attached to a whitespace character in the text, the segment
//     ends after the diacritic, so e.g. " ̀ " becomes two segments.
//
// This function therefore runs the segmenter's .segment() method and then merges
// consecutive segments of whitespace into a single part.
func segment(str string, segmenter Segmenter) []string {
	var parts []string
	for _, seg := range segmenter.Segment(str) {
		if len(parts) > 0 && containsJSSpace(parts[len(parts)-1]) && containsJSSpace(seg) {
			parts[len(parts)-1] += seg
		} else {
			parts = append(parts, seg)
		}
	}
	return parts
}

// The functions below take a segmenter argument so that, when called from diffWords
// when it is using a segmenter, they can use a notion of what constitutes "whitespace"
// that is consistent with the segmenter.

func trailingWs(str string, segmenter Segmenter) string {
	if segmenter != nil {
		_, tail := leadingAndTrailingWs(str, segmenter)
		return tail
	}
	return str[trailingSpaceStart(str):]
}

func leadingWs(str string, segmenter Segmenter) string {
	if segmenter != nil {
		head, _ := leadingAndTrailingWs(str, segmenter)
		return head
	}
	return str[:leadingSpaceLen(str)]
}

func leadingAndTrailingWs(str string, segmenter Segmenter) (string, string) {
	if segmenter == nil {
		return leadingWs(str, nil), trailingWs(str, nil)
	}
	if segmenter.Granularity() != "word" {
		panic(segmenterGranularityError)
	}
	segments := segment(str, segmenter)
	head, tail := "", ""
	if len(segments) > 0 {
		if containsJSSpace(segments[0]) {
			head = segments[0]
		}
		if last := segments[len(segments)-1]; containsJSSpace(last) {
			tail = last
		}
	}
	return head, tail
}
