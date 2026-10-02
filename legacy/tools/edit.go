package tools

import (
	"context"
	"fmt"
	"os"
	"strings"

	ag "github.com/keejkrej/pi-go/legacy/agentloop"
)

// NewEditTool returns a tool that replaces occurrences of old_string with
// new_string in a file. When replace_all is false (the default), old_string
// must occur exactly once. Relative paths are resolved against cwd.
func NewEditTool(cwd string) ag.AgentTool {
	return &ag.FuncTool{
		NameVal:        "edit",
		LabelVal:       "Edit",
		DescriptionVal: "Replace a string in a file. By default old_string must appear exactly once; set replace_all to replace every occurrence.",
		ParametersVal: ObjectSchema(map[string]any{
			"path":        StringProp("Path to the file to edit (relative paths are resolved against the working directory)."),
			"old_string":  StringProp("The exact string to replace."),
			"new_string":  StringProp("The replacement string."),
			"replace_all": BoolProp("Replace every occurrence instead of requiring a unique match."),
		}, "path", "old_string", "new_string"),
		Mode: ag.ExecutionSequential,
		ExecuteFn: func(ctx context.Context, _ string, params map[string]any, _ ag.UpdateFunc) (ag.ToolResult, error) {
			if err := ctx.Err(); err != nil {
				return ag.ToolResult{}, err
			}
			path := resolvePath(cwd, stringArg(params, "path"))
			oldStr := stringArg(params, "old_string")
			newStr := stringArg(params, "new_string")
			replaceAll := boolArg(params, "replace_all")

			data, err := os.ReadFile(path)
			if err != nil {
				return ag.ToolResult{}, err
			}
			text := string(data)

			count := strings.Count(text, oldStr)
			var updated string
			if replaceAll {
				if count == 0 {
					return ag.ToolResult{}, fmt.Errorf("old_string not found in %s", path)
				}
				updated = strings.ReplaceAll(text, oldStr, newStr)
			} else {
				switch count {
				case 0:
					return ag.ToolResult{}, fmt.Errorf("old_string not found in %s", path)
				case 1:
					updated = strings.Replace(text, oldStr, newStr, 1)
				default:
					return ag.ToolResult{}, fmt.Errorf("old_string appears %d times in %s; provide a more specific string or set replace_all", count, path)
				}
			}

			if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
				return ag.ToolResult{}, err
			}

			replaced := count
			if !replaceAll {
				replaced = 1
			}
			return ag.TextResult(fmt.Sprintf("Replaced %d occurrence(s) in %s", replaced, path)), nil
		},
	}
}
