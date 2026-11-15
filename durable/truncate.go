// Ported from packages/durable/src/truncate.ts (pi v1.0.0).

package durable

import (
	"fmt"
	"strings"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

const (
	// DefaultMaxLines is the default maximum line limit for truncation helpers.
	DefaultMaxLines = 2000
	// DefaultMaxBytes is the default maximum byte limit (50KB) for truncation helpers.
	DefaultMaxBytes = 50 * 1024
)

// TruncationResult describes the outcome and metrics of truncating text content.
type TruncationResult struct {
	// Content is the truncated content.
	Content string `json:"content"`
	// Truncated reports whether truncation occurred.
	Truncated bool `json:"truncated"`
	// TruncatedBy names the limit hit ("lines", "bytes") or is null if not truncated.
	TruncatedBy jsonx.Opt[string] `json:"truncatedBy"`
	// TotalLines is the total number of lines in the original content.
	TotalLines int `json:"totalLines"`
	// TotalBytes is the total number of bytes in the original content.
	TotalBytes int `json:"totalBytes"`
	// OutputLines is the number of complete lines in the truncated output.
	OutputLines int `json:"outputLines"`
	// OutputBytes is the number of bytes in the truncated output.
	OutputBytes int `json:"outputBytes"`
	// LastLinePartial reports whether the last line was partially truncated (tail truncation only).
	LastLinePartial bool `json:"lastLinePartial"`
	// FirstLineExceedsLimit reports whether the first line exceeded maxBytes (head truncation).
	FirstLineExceedsLimit bool `json:"firstLineExceedsLimit"`
	// MaxLines is the maxLines limit that was applied.
	MaxLines int `json:"maxLines"`
	// MaxBytes is the maxBytes limit that was applied.
	MaxBytes int `json:"maxBytes"`
}

// TruncationOptions configures line and byte limits for truncation.
type TruncationOptions struct {
	MaxLines *int `json:"maxLines,omitzero"`
	MaxBytes *int `json:"maxBytes,omitzero"`
}

// Utf8ByteLength returns the UTF-8 byte length of content.
func Utf8ByteLength(content string) int {
	return len(content)
}

func trunSplitLinesForCounting(content string) []string {
	if len(content) == 0 {
		return nil
	}
	lines := strings.Split(content, "\n")
	if strings.HasSuffix(content, "\n") {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// FormatSize formats bytes as a human-readable B, KB, or MB string.
func FormatSize(bytes int) string {
	if bytes < 1024 {
		return fmt.Sprintf("%dB", bytes)
	}
	if bytes < 1024*1024 {
		return fmt.Sprintf("%.1fKB", float64(bytes)/1024)
	}
	return fmt.Sprintf("%.1fMB", float64(bytes)/(1024*1024))
}

func trunResolveOptions(options []*TruncationOptions) (int, int) {
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

// TruncateHead truncates content from the head, keeping at most maxLines lines and maxBytes bytes.
// Never returns partial lines; if the first line exceeds maxBytes, Content is empty and FirstLineExceedsLimit is true.
func TruncateHead(content string, options ...*TruncationOptions) TruncationResult {
	maxLines, maxBytes := trunResolveOptions(options)

	totalBytes := Utf8ByteLength(content)
	lines := trunSplitLinesForCounting(content)
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

	if len(lines) > 0 && Utf8ByteLength(lines[0]) > maxBytes {
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
		lineBytes := Utf8ByteLength(line)
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

	// Without a byte break, only omitted lines prove the line limit was reached; otherwise a trailing newline exceeded bytes.
	if truncatedBy != "bytes" {
		if len(outputLinesArr) < totalLines {
			truncatedBy = "lines"
		} else {
			truncatedBy = "bytes"
		}
	}

	outputContent := strings.Join(outputLinesArr, "\n")
	finalOutputBytes := Utf8ByteLength(outputContent)

	return TruncationResult{
		Content:               outputContent,
		Truncated:             true,
		TruncatedBy:           jsonx.Some(truncatedBy),
		TotalLines:            totalLines,
		TotalBytes:            totalBytes,
		OutputLines:           len(outputLinesArr),
		OutputBytes:           finalOutputBytes,
		LastLinePartial:       false,
		FirstLineExceedsLimit: false,
		MaxLines:              maxLines,
		MaxBytes:              maxBytes,
	}
}
