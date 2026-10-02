// Ported from get-east-asian-width@1.6.0 index.js, lookup.js and utilities.js.

package eastasian

// WidthType is the East Asian Width category of a code point (TS `WidthType`).
type WidthType string

// The East Asian Width categories.
const (
	WidthTypeFullwidth WidthType = "fullwidth"
	WidthTypeHalfwidth WidthType = "halfwidth"
	WidthTypeWide      WidthType = "wide"
	WidthTypeNarrow    WidthType = "narrow"
	WidthTypeNeutral   WidthType = "neutral"
	WidthTypeAmbiguous WidthType = "ambiguous"
)

// Width returns the width (1 or 2) of the given code point (TS `eastAsianWidth`).
//
// Fullwidth and wide characters are 2 columns wide. Ambiguous characters are 2 columns wide only
// when ambiguousAsWide is true (TS option `ambiguousAsWide`, default false); everything else is 1.
// Values outside the Unicode range, including negative ones, are 1, as in TS.
func Width(r rune, ambiguousAsWide bool) int {
	if IsFullWidth(r) || IsWide(r) || (ambiguousAsWide && IsAmbiguous(r)) {
		return 2
	}
	return 1
}

// EastAsianWidthType returns the East Asian Width category of the given code point (TS
// `eastAsianWidthType`).
func EastAsianWidthType(r rune) WidthType {
	if IsAmbiguous(r) {
		return WidthTypeAmbiguous
	}
	if IsFullWidth(r) {
		return WidthTypeFullwidth
	}
	if isHalfWidth(r) {
		return WidthTypeHalfwidth
	}
	if isNarrow(r) {
		return WidthTypeNarrow
	}
	if IsWide(r) {
		return WidthTypeWide
	}
	return WidthTypeNeutral
}

// IsAmbiguous reports whether r has East Asian Width "A".
func IsAmbiguous(r rune) bool {
	if r < ambiguousMinimalCodePoint || r > ambiguousMaximumCodePoint {
		return false
	}
	return isInRange(ambiguousRanges, r)
}

// IsFullWidth reports whether r has East Asian Width "F" (TS private export `_isFullWidth`).
func IsFullWidth(r rune) bool {
	if r < fullwidthMinimalCodePoint || r > fullwidthMaximumCodePoint {
		return false
	}
	return isInRange(fullwidthRanges, r)
}

func isHalfWidth(r rune) bool {
	if r < halfwidthMinimalCodePoint || r > halfwidthMaximumCodePoint {
		return false
	}
	return isInRange(halfwidthRanges, r)
}

func isNarrow(r rune) bool {
	if r < narrowMinimalCodePoint || r > narrowMaximumCodePoint {
		return false
	}
	return isInRange(narrowRanges, r)
}

// commonCjkCodePoint is the start of the CJK Unified Ideographs block.
const commonCjkCodePoint = 0x4E00

// wideFastPathStart and wideFastPathEnd bound a hot-path range so common IsWide calls can skip
// the binary search: the range containing U+4E00 covers common CJK ideographs, with the largest
// range as fallback for resilience to Unicode table changes.
var wideFastPathStart, wideFastPathEnd = findWideFastPathRange(wideRanges)

func findWideFastPathRange(ranges []rune) (rune, rune) {
	fastPathStart, fastPathEnd := ranges[0], ranges[1]
	for index := 0; index < len(ranges); index += 2 {
		start, end := ranges[index], ranges[index+1]
		if commonCjkCodePoint >= start && commonCjkCodePoint <= end {
			return start, end
		}
		if end-start > fastPathEnd-fastPathStart {
			fastPathStart, fastPathEnd = start, end
		}
	}
	return fastPathStart, fastPathEnd
}

// IsWide reports whether r has East Asian Width "W" (TS private export `_isWide`).
func IsWide(r rune) bool {
	if r >= wideFastPathStart && r <= wideFastPathEnd {
		return true
	}
	if r < wideMinimalCodePoint || r > wideMaximumCodePoint {
		return false
	}
	return isInRange(wideRanges, r)
}

// isInRange is a binary search on a sorted flat list of inclusive [start, end] pairs.
func isInRange(ranges []rune, r rune) bool {
	low := 0
	high := len(ranges)/2 - 1
	for low <= high {
		mid := (low + high) / 2
		i := mid * 2
		if r < ranges[i] {
			high = mid - 1
		} else if r > ranges[i+1] {
			low = mid + 1
		} else {
			return true
		}
	}
	return false
}
