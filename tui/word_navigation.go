// Ported from packages/tui/src/word-navigation.ts (pi v1.0.0).

package tui

// WordNavigationOptions selects how word boundaries are found.
// A nil options pointer, or a nil Segment, uses GetWordSegmenter.
type WordNavigationOptions struct {
	// Segment returns the word segments of text.
	Segment func(text string) []SegmentData
	// IsAtomicSegment reports a segment that moves as one unit, such as a paste marker.
	IsAtomicSegment func(segment string) bool
}

// FindWordBackward returns the cursor after moving one word backward from cursor.
// cursor is a UTF-16 index into text. The result is never negative.
func FindWordBackward(text string, cursor int, options *WordNavigationOptions) int {
	panic("unported: FindWordBackward")
}

// FindWordForward returns the cursor after moving one word forward from cursor.
// cursor is a UTF-16 index into text. The result is at most the UTF-16 length of text.
func FindWordForward(text string, cursor int, options *WordNavigationOptions) int {
	panic("unported: FindWordForward")
}
