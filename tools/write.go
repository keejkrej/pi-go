package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	ag "github.com/keejkrej/pi-go/agentloop"
)

// NewWriteTool returns a tool that writes content to a file, creating parent
// directories as needed. Relative paths are resolved against cwd.
func NewWriteTool(cwd string) ag.AgentTool {
	return &ag.FuncTool{
		NameVal:        "write",
		LabelVal:       "Write",
		DescriptionVal: "Write content to a file, creating parent directories as needed and overwriting any existing file.",
		ParametersVal: ObjectSchema(map[string]any{
			"path":    StringProp("Path to the file to write (relative paths are resolved against the working directory)."),
			"content": StringProp("The full content to write to the file."),
		}, "path", "content"),
		Mode: ag.ExecutionSequential,
		ExecuteFn: func(ctx context.Context, _ string, params map[string]any, _ ag.UpdateFunc) (ag.ToolResult, error) {
			if err := ctx.Err(); err != nil {
				return ag.ToolResult{}, err
			}
			path := resolvePath(cwd, stringArg(params, "path"))
			content := stringArg(params, "content")

			if dir := filepath.Dir(path); dir != "" {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					return ag.ToolResult{}, err
				}
			}
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				return ag.ToolResult{}, err
			}

			return ag.TextResult(fmt.Sprintf("Wrote %d bytes to %s", len(content), path)), nil
		},
	}
}
