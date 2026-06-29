package tools

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	ag "github.com/keejkrej/pi-go/agentloop"
)

// maxGrepMatches caps the number of matching lines reported by grep.
const maxGrepMatches = 200

// NewGrepTool returns a tool that searches files under a directory for lines
// matching a Go regular expression, optionally filtered by an include glob on
// the file basename. Relative paths are resolved against cwd.
func NewGrepTool(cwd string) ag.AgentTool {
	return &ag.FuncTool{
		NameVal:        "grep",
		LabelVal:       "Grep",
		DescriptionVal: "Search files for lines matching a regular expression, returning file:line:text matches.",
		ParametersVal: ObjectSchema(map[string]any{
			"pattern": StringProp("Go regular expression to search for."),
			"path":    StringProp("Directory to search under (relative paths are resolved against the working directory; defaults to the working directory)."),
			"include": StringProp("Optional glob matched against each file's basename (e.g. \"*.go\")."),
		}, "pattern"),
		Mode: ag.ExecutionParallel,
		ExecuteFn: func(ctx context.Context, _ string, params map[string]any, _ ag.UpdateFunc) (ag.ToolResult, error) {
			if err := ctx.Err(); err != nil {
				return ag.ToolResult{}, err
			}
			pattern := stringArg(params, "pattern")
			re, err := regexp.Compile(pattern)
			if err != nil {
				return ag.ToolResult{}, fmt.Errorf("invalid pattern: %w", err)
			}
			include := stringArg(params, "include")

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
				if include != "" {
					ok, mErr := filepath.Match(include, d.Name())
					if mErr != nil {
						return fmt.Errorf("invalid include glob: %w", mErr)
					}
					if !ok {
						return nil
					}
				}

				data, rErr := os.ReadFile(p)
				if rErr != nil {
					// Skip unreadable files rather than aborting the walk.
					return nil
				}
				if looksBinary(data) {
					return nil
				}

				rel, relErr := filepath.Rel(cwd, p)
				if relErr != nil {
					rel = p
				}

				scanner := bufio.NewScanner(bytes.NewReader(data))
				scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
				lineNo := 0
				for scanner.Scan() {
					lineNo++
					line := scanner.Text()
					if re.MatchString(line) {
						matches = append(matches, fmt.Sprintf("%s:%d:%s", rel, lineNo, line))
						if len(matches) >= maxGrepMatches {
							truncated = true
							return filepath.SkipAll
						}
					}
				}
				return nil
			})
			if walkErr != nil {
				return ag.ToolResult{}, walkErr
			}

			if len(matches) == 0 {
				return ag.TextResult("No matches found."), nil
			}
			out := strings.Join(matches, "\n")
			if truncated {
				out += fmt.Sprintf("\n\n[Truncated: more than %d matches]", maxGrepMatches)
			}
			return ag.TextResult(out), nil
		},
	}
}

// looksBinary reports whether data appears to be a binary (non-text) file,
// using a NUL-byte heuristic over a prefix.
func looksBinary(data []byte) bool {
	n := len(data)
	if n > 8000 {
		n = 8000
	}
	return bytes.IndexByte(data[:n], 0) >= 0
}
