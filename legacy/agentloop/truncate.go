package agentloop

import (
	"strings"
	"unicode/utf8"
)

// Default truncation limits (port of pi's harness/utils/truncate.ts).
const (
	DefaultTruncateMaxLines = 2000
	DefaultTruncateMaxBytes = 50 * 1024
)

// TruncateOptions bound a tool output by lines and bytes; whichever limit is
// hit first wins. Zero fields take the defaults; negative fields disable that
// limit.
type TruncateOptions struct {
	MaxLines int
	MaxBytes int
}

// TruncationResult describes a truncation outcome.
type TruncationResult struct {
	Content     string
	Truncated   bool
	TruncatedBy string // "lines", "bytes", or ""
	TotalLines  int
	TotalBytes  int
	OutputLines int
	OutputBytes int
	// LastLinePartial is true when the byte limit cut a line in half (head:
	// the last kept line; tail: the first kept line).
	LastLinePartial bool
}

func (o TruncateOptions) resolve() (maxLines, maxBytes int) {
	maxLines, maxBytes = o.MaxLines, o.MaxBytes
	if maxLines == 0 {
		maxLines = DefaultTruncateMaxLines
	}
	if maxBytes == 0 {
		maxBytes = DefaultTruncateMaxBytes
	}
	return maxLines, maxBytes
}

// TruncateHead keeps the beginning of content (use for file reads).
func TruncateHead(content string, opts TruncateOptions) TruncationResult {
	maxLines, maxBytes := opts.resolve()
	lines := strings.Split(content, "\n")
	res := TruncationResult{Content: content, TotalLines: len(lines), TotalBytes: len(content)}

	if maxLines > 0 && len(lines) > maxLines {
		res.Content = strings.Join(lines[:maxLines], "\n")
		res.Truncated = true
		res.TruncatedBy = "lines"
	}
	if maxBytes > 0 && len(res.Content) > maxBytes {
		res.Content = truncateAtRuneBoundary(res.Content, maxBytes)
		res.Truncated = true
		res.TruncatedBy = "bytes"
		res.LastLinePartial = true
	}
	res.OutputBytes = len(res.Content)
	res.OutputLines = countLines(res.Content)
	return res
}

// TruncateTail keeps the end of content (use for shell output).
func TruncateTail(content string, opts TruncateOptions) TruncationResult {
	maxLines, maxBytes := opts.resolve()
	lines := strings.Split(content, "\n")
	res := TruncationResult{Content: content, TotalLines: len(lines), TotalBytes: len(content)}

	if maxLines > 0 && len(lines) > maxLines {
		res.Content = strings.Join(lines[len(lines)-maxLines:], "\n")
		res.Truncated = true
		res.TruncatedBy = "lines"
	}
	if maxBytes > 0 && len(res.Content) > maxBytes {
		cut := res.Content[len(res.Content)-maxBytes:]
		// Advance to a rune boundary.
		for len(cut) > 0 && !utf8.RuneStart(cut[0]) {
			cut = cut[1:]
		}
		res.Content = cut
		res.Truncated = true
		res.TruncatedBy = "bytes"
		res.LastLinePartial = true
	}
	res.OutputBytes = len(res.Content)
	res.OutputLines = countLines(res.Content)
	return res
}

// TruncateLine caps a single line's length (use for grep-style match lines).
func TruncateLine(line string, maxLen int) string {
	if maxLen <= 0 || len(line) <= maxLen {
		return line
	}
	return truncateAtRuneBoundary(line, maxLen) + "..."
}

func truncateAtRuneBoundary(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	cut := s[:maxBytes]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut
}

func countLines(s string) int {
	if s == "" {
		return 1
	}
	return strings.Count(s, "\n") + 1
}
