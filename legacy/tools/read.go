package tools

import (
	"context"
	"fmt"
	"os"
	"strings"

	ag "github.com/keejkrej/pi-go/legacy/agentloop"
)

const (
	// defaultReadLineLimit caps the number of lines returned by a read.
	defaultReadLineLimit = 2000
	// maxReadBytes caps the total bytes returned by a read.
	maxReadBytes = 256 * 1024
)

// NewReadTool returns a tool that reads a file (relative paths resolved against
// cwd), applying an optional 1-indexed line offset and line limit, and
// truncating large output.
func NewReadTool(cwd string) ag.AgentTool {
	return &ag.FuncTool{
		NameVal:        "read",
		LabelVal:       "Read",
		DescriptionVal: "Read the contents of a file. Supports an optional 1-indexed line offset and a line limit.",
		ParametersVal: ObjectSchema(map[string]any{
			"path":   StringProp("Path to the file to read (relative paths are resolved against the working directory)."),
			"offset": NumberProp("1-indexed line number to start reading from."),
			"limit":  NumberProp("Maximum number of lines to read."),
		}, "path"),
		Mode: ag.ExecutionParallel,
		ExecuteFn: func(ctx context.Context, _ string, params map[string]any, _ ag.UpdateFunc) (ag.ToolResult, error) {
			if err := ctx.Err(); err != nil {
				return ag.ToolResult{}, err
			}
			path := resolvePath(cwd, stringArg(params, "path"))

			data, err := os.ReadFile(path)
			if err != nil {
				return ag.ToolResult{}, err
			}

			text := string(data)
			// Normalize trailing newline handling so the last line isn't an
			// empty phantom entry when the file ends in "\n".
			trimmedNewline := strings.HasSuffix(text, "\n")
			body := text
			if trimmedNewline {
				body = text[:len(text)-1]
			}
			var lines []string
			if body == "" && !trimmedNewline {
				lines = nil
			} else {
				lines = strings.Split(body, "\n")
			}

			offset := 1
			if v, ok := numberArg(params, "offset"); ok && v >= 1 {
				offset = int(v)
			}
			if offset > len(lines) && len(lines) > 0 {
				return ag.ToolResult{}, fmt.Errorf("offset %d is past end of file (%d lines)", offset, len(lines))
			}

			start := offset - 1
			if start < 0 {
				start = 0
			}
			selected := lines[start:]

			limit := defaultReadLineLimit
			if v, ok := numberArg(params, "limit"); ok && v >= 1 {
				limit = int(v)
			}

			truncatedLines := false
			if len(selected) > limit {
				selected = selected[:limit]
				truncatedLines = true
			}

			out := strings.Join(selected, "\n")
			truncatedBytes := false
			if len(out) > maxReadBytes {
				out = out[:maxReadBytes]
				truncatedBytes = true
			}

			var note string
			switch {
			case truncatedBytes:
				note = fmt.Sprintf("\n\n[Truncated: output exceeded %d bytes]", maxReadBytes)
			case truncatedLines:
				note = fmt.Sprintf("\n\n[Truncated: output exceeded %d lines]", limit)
			}

			return ag.TextResult(out + note), nil
		},
	}
}
