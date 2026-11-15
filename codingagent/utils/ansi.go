// Ported from packages/coding-agent/src/utils/ansi.ts (pi v1.5.2).

package utils

import (
	"strings"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsre"
)

const (
	ansiST               = "(?:\\u0007|\\u001B\\u005C|\\u009C)"
	ansiOSCStart         = "\\u001B\\]"
	ansiCSIStart         = "(?:(?:\\u001B\\[|\\u009B)[<=>]|[\\u001B\\u009B][[\\]()#;?]*)(?:\\d{1,4}(?:[;:]\\d{0,4})*)?"
	ansiCSIFinal         = "[\\dA-PR-TZcf-nq-uy=><~]"
	ansiMaxPendingLength = 256
)

var (
	ansiStripRegex           = jsre.MustCompile("(?:\\u001B\\][\\s\\S]*?"+ansiST+")|"+ansiCSIStart+ansiCSIFinal, "g")
	ansiUnfinishedAtEndRegex = jsre.MustCompile(
		"(?:\\u001B\\](?:[^\\u0007\\u009C\\u001B]|\\u001B(?!\\\\))*|"+ansiCSIStart+")$",
		"",
	)
)

// AnsiSuffixSplit holds the complete prefix safe for StripAnsi and any trailing unfinished escape sequence.
type AnsiSuffixSplit struct {
	Complete string
	Pending  string
}

func ansiHasIntroducer(value string) bool {
	return strings.ContainsRune(value, '\u001B') || strings.ContainsRune(value, '\u009B')
}

// SplitIncompleteAnsiSuffix splits streamed text into a complete prefix and a trailing
// unfinished escape sequence that should be prepended to the next chunk.
func SplitIncompleteAnsiSuffix(value string) AnsiSuffixSplit {
	if !ansiHasIntroducer(value) {
		return AnsiSuffixSplit{Complete: value, Pending: ""}
	}

	u16Len := js.Len(value)
	windowStart := max(0, u16Len-ansiMaxPendingLength)
	windowByteStart := js.U16ToByte(value, windowStart)
	tail := value[windowByteStart:]

	match := ansiUnfinishedAtEndRegex.Exec(tail, 0)
	if match == nil {
		return AnsiSuffixSplit{Complete: value, Pending: ""}
	}

	matchByteOffset := js.U16ToByte(tail, match.Index)
	splitAt := windowByteStart + matchByteOffset
	return AnsiSuffixSplit{
		Complete: value[:splitAt],
		Pending:  value[splitAt:],
	}
}

// StripAnsi removes ANSI escape sequences from value.
func StripAnsi(value string) string {
	if !ansiHasIntroducer(value) {
		return value
	}
	return ansiStripRegex.Replace(value, "")
}
