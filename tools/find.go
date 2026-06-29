package tools

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	ag "github.com/keejkrej/pi-go/agentloop"
)

// maxFindMatches caps the number of paths reported by find.
const maxFindMatches = 200

// NewFindTool returns a tool that walks files under a directory and returns the
// relative paths whose basename matches a glob pattern. Relative paths are
// resolved against cwd.
func NewFindTool(cwd string) ag.AgentTool {
	return &ag.FuncTool{
		NameVal:        "find",
		LabelVal:       "Find",
		DescriptionVal: "Find files whose basename matches a glob pattern, returning matching paths.",
		ParametersVal: ObjectSchema(map[string]any{
			"pattern": StringProp("Glob pattern matched against each file's basename (e.g. \"*.go\")."),
			"path":    StringProp("Directory to search under (relative paths are resolved against the working directory; defaults to the working directory)."),
		}, "pattern"),
		Mode: ag.ExecutionParallel,
		ExecuteFn: func(ctx context.Context, _ string, params map[string]any, _ ag.UpdateFunc) (ag.ToolResult, error) {
			if err := ctx.Err(); err != nil {
				return ag.ToolResult{}, err
			}
			pattern := stringArg(params, "pattern")
			// Validate the glob up front so a bad pattern is a clean error.
			if _, err := filepath.Match(pattern, ""); err != nil {
				return ag.ToolResult{}, fmt.Errorf("invalid pattern: %w", err)
			}

			root := cwd
			if p := stringArg(params, "path"); p != "" {
				root = resolvePath(cwd, p)
			}

			var matches []string
			truncated := false

			walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if cerr := ctx.Err(); cerr != nil {
					return cerr
				}
				if d.IsDir() {
					if ignoreDirs[d.Name()] {
						return filepath.SkipDir
					}
					return nil
				}
				ok, mErr := filepath.Match(pattern, d.Name())
				if mErr != nil {
					return fmt.Errorf("invalid pattern: %w", mErr)
				}
				if !ok {
					return nil
				}
				rel, relErr := filepath.Rel(cwd, p)
				if relErr != nil {
					rel = p
				}
				matches = append(matches, rel)
				if len(matches) >= maxFindMatches {
					truncated = true
					return filepath.SkipAll
				}
				return nil
			})
			if walkErr != nil {
				return ag.ToolResult{}, walkErr
			}

			if len(matches) == 0 {
				return ag.TextResult("No files found."), nil
			}
			out := strings.Join(matches, "\n")
			if truncated {
				out += fmt.Sprintf("\n\n[Truncated: more than %d matches]", maxFindMatches)
			}
			return ag.TextResult(out), nil
		},
	}
}
