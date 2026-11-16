// Ported from packages/mcp/src/output-limit.ts (pi v1.5.2).

package mcp

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/keejkrej/pi-go/mcp/protocol"
)

// McpOutputMaxBytes is the maximum byte budget for combined MCP tool text
// output before middle truncation (20 KB).
const McpOutputMaxBytes = 20 * 1024

// McpOutputSaver persists overflowing tool output and returns its file path.
type McpOutputSaver func(ctx context.Context, data []byte, extension string) (string, error)

// LimitedMcpContent holds size-bounded model-facing MCP tool output and an
// optional path where the untruncated text was saved.
type LimitedMcpContent struct {
	Content        []protocol.LlmContent `json:"content"`
	FullOutputPath *string               `json:"fullOutputPath,omitzero"`
}

// LimitMcpContent bounds the text blocks of content to McpOutputMaxBytes by
// keeping the head and tail around a truncation marker and optionally saving the
// full combined text via saveOutput.
func LimitMcpContent(ctx context.Context, content []protocol.LlmContent, saveOutput McpOutputSaver) LimitedMcpContent {
	var textParts []string
	var nonText []protocol.LlmContent
	for _, item := range content {
		if t, ok := item.(*protocol.LlmTextContent); ok && t != nil {
			textParts = append(textParts, t.Text)
		} else if item != nil {
			nonText = append(nonText, item)
		}
	}

	combined := strings.Join(textParts, "\n")
	if len(combined) <= McpOutputMaxBytes {
		return LimitedMcpContent{Content: content}
	}

	buf := []byte(combined)
	truncated := outlTruncateMiddle(buf, McpOutputMaxBytes)
	totalLines := strings.Count(combined, "\n") + 1
	if strings.HasSuffix(combined, "\n") {
		totalLines--
	}
	tokens := (len(buf) + 3) / 4

	var fullOutputPath *string
	var where string
	if saveOutput != nil {
		path, err := saveOutput(ctx, buf, ".txt")
		if err != nil {
			where = fmt.Sprintf("\n\n[Could not save the full output: %s]", err.Error())
		} else {
			fullOutputPath = &path
			where = fmt.Sprintf("\n\n[Full output: %s (read it with offset/limit)]", path)
		}
	}

	text := fmt.Sprintf(
		"Warning: truncated output (original token count: %d)\nTotal output lines: %d\n\n%s%s",
		tokens,
		totalLines,
		truncated,
		where,
	)

	out := make([]protocol.LlmContent, 0, 1+len(nonText))
	out = append(out, &protocol.LlmTextContent{
		Type: "text",
		Text: text,
	})
	out = append(out, nonText...)
	return LimitedMcpContent{
		Content:        out,
		FullOutputPath: fullOutputPath,
	}
}

func outlTruncateMiddle(buf []byte, maxBytes int) string {
	headTarget := maxBytes / 2
	tailTarget := maxBytes - headTarget

	headEnd := headTarget
	for headEnd > 0 && (buf[headEnd]&0xc0) == 0x80 {
		headEnd--
	}

	tailStart := len(buf) - tailTarget
	for tailStart < len(buf) && (buf[tailStart]&0xc0) == 0x80 {
		tailStart++
	}

	head := string(buf[:headEnd])
	tail := string(buf[tailStart:])
	removedChars := utf8.RuneCount(buf[headEnd:tailStart])
	return fmt.Sprintf("%s…%d chars truncated…%s", head, removedChars, tail)
}
