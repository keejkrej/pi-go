// Ported from packages/tui/src/word-navigation.ts (pi v1.5.2).

package tui

import (
	"unicode/utf8"

	"github.com/keejkrej/pi-go/internal/js"
)

// WordNavigationOptions selects how word boundaries are found.
// A nil options pointer, or a nil Segment, uses GetWordSegmenter.
type WordNavigationOptions struct {
	// Segment returns the word segments of text.
	Segment func(text string) []SegmentData
	// IsAtomicSegment reports a segment that moves as one unit, such as a paste marker.
	IsAtomicSegment func(segment string) bool
}

type wnSegment struct {
	text         string
	start        int
	end          int
	isWordLike   bool
	isWhitespace bool
	isAtomic     bool
}

func wnNormalizeSegments(text string, options *WordNavigationOptions) []wnSegment {
	var raw []SegmentData
	if options != nil && options.Segment != nil {
		raw = options.Segment(text)
	} else {
		raw = GetWordSegmenter().Segment(text)
	}
	var out []wnSegment
	for _, seg := range raw {
		if seg.Segment == "" {
			continue
		}
		atomic := options != nil && options.IsAtomicSegment != nil && options.IsAtomicSegment(seg.Segment)
		if atomic {
			l := js.Len(seg.Segment)
			out = append(out, wnSegment{
				text:         seg.Segment,
				start:        seg.Index,
				end:          seg.Index + l,
				isWordLike:   true,
				isWhitespace: false,
				isAtomic:     true,
			})
			continue
		}
		wordLike := seg.IsWordLike != nil && *seg.IsWordLike
		if wordLike && wnContainsPunctuation(seg.Segment) {
			out = append(out, wnSplitOnPunctuation(seg.Segment, seg.Index)...)
			continue
		}
		l := js.Len(seg.Segment)
		out = append(out, wnSegment{
			text:         seg.Segment,
			start:        seg.Index,
			end:          seg.Index + l,
			isWordLike:   wordLike,
			isWhitespace: IsWhitespaceChar(seg.Segment),
			isAtomic:     false,
		})
	}
	return out
}

func wnContainsPunctuation(s string) bool {
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if IsPunctuationChar(s[i : i+size]) {
			_ = r
			return true
		}
		i += size
	}
	return false
}

func wnSplitOnPunctuation(s string, baseU16 int) []wnSegment {
	var out []wnSegment
	offset := baseU16
	runStart := 0
	runU16 := 0
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		ch := s[i : i+size]
		chU16 := 1
		if r > 0xffff {
			chU16 = 2
		}
		if IsPunctuationChar(ch) {
			if i > runStart {
				sub := s[runStart:i]
				out = append(out, wnSegment{
					text:       sub,
					start:      offset,
					end:        offset + runU16,
					isWordLike: true,
				})
				offset += runU16
				runU16 = 0
			}
			out = append(out, wnSegment{
				text:       ch,
				start:      offset,
				end:        offset + chU16,
				isWordLike: false,
			})
			offset += chU16
			i += size
			runStart = i
			continue
		}
		runU16 += chU16
		i += size
	}
	if runStart < len(s) {
		sub := s[runStart:]
		out = append(out, wnSegment{
			text:       sub,
			start:      offset,
			end:        offset + runU16,
			isWordLike: true,
		})
	}
	return out
}

// FindWordBackward returns the cursor after moving one word backward from cursor.
// cursor is a UTF-16 index into text. The result is never negative.
func FindWordBackward(text string, cursor int, options *WordNavigationOptions) int {
	if cursor <= 0 || text == "" {
		return 0
	}
	utf16Len := js.Len(text)
	c := cursor
	if c > utf16Len {
		c = utf16Len
	}
	segs := wnNormalizeSegments(text, options)
	i := len(segs) - 1
	for i >= 0 && segs[i].start >= c {
		i--
	}
	for i >= 0 && segs[i].isWhitespace {
		i--
	}
	if i < 0 {
		return 0
	}
	if segs[i].isAtomic || segs[i].isWordLike {
		return segs[i].start
	}
	targetStart := segs[i].start
	for i > 0 && !segs[i-1].isWhitespace && !segs[i-1].isWordLike && !segs[i-1].isAtomic {
		i--
		targetStart = segs[i].start
	}
	return targetStart
}

// FindWordForward returns the cursor after moving one word forward from cursor.
// cursor is a UTF-16 index into text. The result is at most the UTF-16 length of text.
func FindWordForward(text string, cursor int, options *WordNavigationOptions) int {
	utf16Len := js.Len(text)
	if cursor >= utf16Len || text == "" {
		return utf16Len
	}
	c := cursor
	if c < 0 {
		c = 0
	}
	segs := wnNormalizeSegments(text, options)
	i := 0
	for i < len(segs) && segs[i].end <= c {
		i++
	}
	for i < len(segs) && segs[i].isWhitespace {
		i++
	}
	if i >= len(segs) {
		return utf16Len
	}
	if segs[i].isAtomic || segs[i].isWordLike {
		return segs[i].end
	}
	targetEnd := segs[i].end
	for i+1 < len(segs) && !segs[i+1].isWhitespace && !segs[i+1].isWordLike && !segs[i+1].isAtomic {
		i++
		targetEnd = segs[i].end
	}
	return targetEnd
}
