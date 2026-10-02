package typebox

import "github.com/keejkrej/pi-go/internal/js"

func between(v, min, max int) bool { return v >= min && v <= max }

func isHighSurrogate(v int) bool { return between(v, 0xD800, 0xDBFF) }

func isRegionalIndicator(v int) bool { return between(v, 0x1F1E6, 0x1F1FF) }

func isVariationSelector(v int) bool { return between(v, 0xFE00, 0xFE0F) }

func isCombiningMark(v int) bool {
	return between(v, 0x0300, 0x036F) ||
		between(v, 0x1AB0, 0x1AFF) ||
		between(v, 0x1DC0, 0x1DFF) ||
		between(v, 0xFE20, 0xFE2F)
}

func codePointLength(v int) int {
	if v > 0xFFFF {
		return 2
	}
	return 1
}

func consumeModifiers(value string, index int) int {
	n := js.Len(value)
	for index < n {
		point := js.CodePointAt(value, index)
		if point < 0 {
			break
		}
		if isCombiningMark(point) || isVariationSelector(point) {
			index += codePointLength(point)
			continue
		}
		break
	}
	return index
}

func nextGraphemeClusterIndex(value string, clusterStart int) int {
	n := js.Len(value)
	startCP := js.CodePointAt(value, clusterStart)
	if startCP < 0 {
		if clusterStart < n {
			return clusterStart + 1
		}
		return n
	}
	clusterEnd := clusterStart + codePointLength(startCP)
	clusterEnd = consumeModifiers(value, clusterEnd)
	for clusterEnd < n-1 && js.CodePointAt(value, clusterEnd) == 0x200D {
		next := js.CodePointAt(value, clusterEnd+1)
		if next < 0 {
			break
		}
		clusterEnd += 1 + codePointLength(next)
		clusterEnd = consumeModifiers(value, clusterEnd)
	}
	if isRegionalIndicator(startCP) && clusterEnd < n {
		cp := js.CodePointAt(value, clusterEnd)
		if isRegionalIndicator(cp) {
			clusterEnd += codePointLength(cp)
		}
	}
	return clusterEnd
}

func isGraphemeCodePoint(value int) bool {
	return value >= 0x0300 && (isHighSurrogate(value) || isCombiningMark(value) || isVariationSelector(value) || value == 0x200D)
}

func isMinLengthSegmented(value string, minLength float64) bool {
	count := 0
	index := 0
	n := js.Len(value)
	for index < n {
		index = nextGraphemeClusterIndex(value, index)
		count++
		if float64(count) >= minLength {
			return true
		}
	}
	return false
}

func isMaxLengthSegmented(value string, maxLength float64) bool {
	count := 0
	index := 0
	n := js.Len(value)
	for index < n {
		index = nextGraphemeClusterIndex(value, index)
		count++
		if float64(count) > maxLength {
			return false
		}
	}
	return true
}

// isMinLength is TypeBox guard/string IsMinLength (grapheme clusters).
func isMinLength(value string, minLength float64) bool {
	if minLength == 0 {
		return true
	}
	if float64(js.Len(value)) < minLength {
		return false
	}
	index := 0
	for {
		if isGraphemeCodePoint(js.CharCodeAt(value, index)) {
			return isMinLengthSegmented(value, minLength)
		}
		index++
		if float64(index) >= minLength {
			return true
		}
	}
}

// isMaxLength is TypeBox guard/string IsMaxLength (grapheme clusters).
func isMaxLength(value string, maxLength float64) bool {
	if float64(js.Len(value)) <= maxLength {
		return true
	}
	index := 0
	for {
		if isGraphemeCodePoint(js.CharCodeAt(value, index)) {
			return isMaxLengthSegmented(value, maxLength)
		}
		index++
		if float64(index) > maxLength {
			return false
		}
	}
}
