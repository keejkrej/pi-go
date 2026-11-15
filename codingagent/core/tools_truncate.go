// Ported from packages/coding-agent/src/core/tools/truncate.ts (pi v1.0.0).

package core

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

const (
	// DefaultMaxLines is the default maximum number of lines kept before truncation.
	DefaultMaxLines = 2000
	// DefaultMaxBytes is the default maximum byte size kept before truncation (50KB).
	DefaultMaxBytes = 50 * 1024
	// GrepMaxLineLength is the default maximum UTF-16 code-unit length for grep match lines.
	GrepMaxLineLength = 500
)

// TruncationResult describes the outcome and metrics of truncating text content.
type TruncationResult struct {
	Content               string            `json:"content"`
	Truncated             bool              `json:"truncated"`
	TruncatedBy           jsonx.Opt[string] `json:"truncatedBy"`
	TotalLines            int               `json:"totalLines"`
	TotalBytes            int               `json:"totalBytes"`
	OutputLines           int               `json:"outputLines"`
	OutputBytes           int               `json:"outputBytes"`
	LastLinePartial       bool              `json:"lastLinePartial"`
	FirstLineExceedsLimit bool              `json:"firstLineExceedsLimit"`
	MaxLines              int               `json:"maxLines"`
	MaxBytes              int               `json:"maxBytes"`
}

// TruncationOptions configures line and byte limits for truncation helpers.
type TruncationOptions struct {
	MaxLines *int
	MaxBytes *int
}

// TruncatedLineResult holds the result of TruncateLine.
type TruncatedLineResult struct {
	Text         string
	WasTruncated bool
}

func ttSplitLinesForCounting(content string) []string {
	if len(content) == 0 {
		return nil
	}
	lines := strings.Split(content, "\n")
	if strings.HasSuffix(content, "\n") {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// FormatSize formats a byte count as a human-readable B / KB / MB string.
func FormatSize(bytes int) string {
	if bytes < 1024 {
		return fmt.Sprintf("%dB", bytes)
	}
	if bytes < 1024*1024 {
		return fmt.Sprintf("%.1fKB", float64(bytes)/1024)
	}
	return fmt.Sprintf("%.1fMB", float64(bytes)/(1024*1024))
}

func ttResolveLimits(options []*TruncationOptions) (int, int) {
	maxLines := DefaultMaxLines
	maxBytes := DefaultMaxBytes
	if len(options) > 0 && options[0] != nil {
		if options[0].MaxLines != nil {
			maxLines = *options[0].MaxLines
		}
		if options[0].MaxBytes != nil {
			maxBytes = *options[0].MaxBytes
		}
	}
	return maxLines, maxBytes
}

// TruncateHead keeps at most maxLines lines / maxBytes bytes from the start of content.
// Never returns partial lines; if the first line exceeds maxBytes, Content is empty and
// FirstLineExceedsLimit is true.
func TruncateHead(content string, options ...*TruncationOptions) TruncationResult {
	maxLines, maxBytes := ttResolveLimits(options)

	totalBytes := len(content)
	lines := ttSplitLinesForCounting(content)
	totalLines := len(lines)

	if totalLines <= maxLines && totalBytes <= maxBytes {
		return TruncationResult{
			Content:               content,
			Truncated:             false,
			TruncatedBy:           jsonx.Null[string](),
			TotalLines:            totalLines,
			TotalBytes:            totalBytes,
			OutputLines:           totalLines,
			OutputBytes:           totalBytes,
			LastLinePartial:       false,
			FirstLineExceedsLimit: false,
			MaxLines:              maxLines,
			MaxBytes:              maxBytes,
		}
	}

	if len(lines) > 0 && len(lines[0]) > maxBytes {
		return TruncationResult{
			Content:               "",
			Truncated:             true,
			TruncatedBy:           jsonx.Some("bytes"),
			TotalLines:            totalLines,
			TotalBytes:            totalBytes,
			OutputLines:           0,
			OutputBytes:           0,
			LastLinePartial:       false,
			FirstLineExceedsLimit: true,
			MaxLines:              maxLines,
			MaxBytes:              maxBytes,
		}
	}

	outputLinesArr := make([]string, 0, min(len(lines), max(0, maxLines)))
	outputBytesCount := 0
	truncatedBy := "lines"

	for i := 0; i < len(lines) && i < maxLines; i++ {
		line := lines[i]
		lineBytes := len(line)
		if i > 0 {
			lineBytes++
		}
		if outputBytesCount+lineBytes > maxBytes {
			truncatedBy = "bytes"
			break
		}
		outputLinesArr = append(outputLinesArr, line)
		outputBytesCount += lineBytes
	}

	if len(outputLinesArr) >= maxLines && outputBytesCount <= maxBytes {
		truncatedBy = "lines"
	}

	outputContent := strings.Join(outputLinesArr, "\n")
	return TruncationResult{
		Content:               outputContent,
		Truncated:             true,
		TruncatedBy:           jsonx.Some(truncatedBy),
		TotalLines:            totalLines,
		TotalBytes:            totalBytes,
		OutputLines:           len(outputLinesArr),
		OutputBytes:           len(outputContent),
		LastLinePartial:       false,
		FirstLineExceedsLimit: false,
		MaxLines:              maxLines,
		MaxBytes:              maxBytes,
	}
}

// TruncateTail keeps at most maxLines lines / maxBytes bytes from the end of content.
// May return a partial first line (with LastLinePartial=true) if the final line alone exceeds maxBytes.
func TruncateTail(content string, options ...*TruncationOptions) TruncationResult {
	maxLines, maxBytes := ttResolveLimits(options)

	totalBytes := len(content)
	lines := ttSplitLinesForCounting(content)
	totalLines := len(lines)

	if totalLines <= maxLines && totalBytes <= maxBytes {
		return TruncationResult{
			Content:               content,
			Truncated:             false,
			TruncatedBy:           jsonx.Null[string](),
			TotalLines:            totalLines,
			TotalBytes:            totalBytes,
			OutputLines:           totalLines,
			OutputBytes:           totalBytes,
			LastLinePartial:       false,
			FirstLineExceedsLimit: false,
			MaxLines:              maxLines,
			MaxBytes:              maxBytes,
		}
	}

	var reversed []string
	outputBytesCount := 0
	truncatedBy := "lines"
	lastLinePartial := false

	for i := len(lines) - 1; i >= 0 && len(reversed) < maxLines; i-- {
		line := lines[i]
		lineBytes := len(line)
		if len(reversed) > 0 {
			lineBytes++
		}
		if outputBytesCount+lineBytes > maxBytes {
			truncatedBy = "bytes"
			if len(reversed) == 0 {
				truncatedLine := ttTruncateStringToBytesFromEnd(line, maxBytes)
				reversed = append(reversed, truncatedLine)
				lastLinePartial = true
			}
			break
		}
		reversed = append(reversed, line)
		outputBytesCount += lineBytes
	}

	slices.Reverse(reversed)

	if len(reversed) >= maxLines && outputBytesCount <= maxBytes {
		truncatedBy = "lines"
	}

	outputContent := strings.Join(reversed, "\n")
	return TruncationResult{
		Content:               outputContent,
		Truncated:             true,
		TruncatedBy:           jsonx.Some(truncatedBy),
		TotalLines:            totalLines,
		TotalBytes:            totalBytes,
		OutputLines:           len(reversed),
		OutputBytes:           len(outputContent),
		LastLinePartial:       lastLinePartial,
		FirstLineExceedsLimit: false,
		MaxLines:              maxLines,
		MaxBytes:              maxBytes,
	}
}

func ttTruncateStringToBytesFromEnd(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	start := len(s) - maxBytes
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return s[start:]
}

// TruncateLine truncates a single line to at most maxChars UTF-16 code units, adding "... [truncated]" if cut.
func TruncateLine(line string, maxChars ...int) TruncatedLineResult {
	limit := GrepMaxLineLength
	if len(maxChars) > 0 {
		limit = maxChars[0]
	}
	if js.Len(line) <= limit {
		return TruncatedLineResult{Text: line, WasTruncated: false}
	}
	return TruncatedLineResult{
		Text:         SliceHead(line, limit) + "... [truncated]",
		WasTruncated: true,
	}
}

// SliceHead returns at most the first maxUnits UTF-16 code units of text, without splitting a surrogate pair.
func SliceHead(text string, maxUnits int) string {
	if maxUnits <= 0 || len(text) == 0 {
		return ""
	}
	used := 0
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		units := 1
		if r >= 0x10000 {
			units = 2
		}
		if used+units > maxUnits {
			return text[:i]
		}
		used += units
		i += size
	}
	return text
}

// SliceTail returns at most the last maxUnits UTF-16 code units of text, without splitting a surrogate pair.
func SliceTail(text string, maxUnits int) string {
	if maxUnits <= 0 || len(text) == 0 {
		return ""
	}
	used := 0
	for i := len(text); i > 0; {
		r, size := utf8.DecodeLastRuneInString(text[:i])
		units := 1
		if r >= 0x10000 {
			units = 2
		}
		if used+units > maxUnits {
			return text[i:]
		}
		used += units
		i -= size
	}
	return text
}
