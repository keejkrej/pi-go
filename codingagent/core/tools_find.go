// Ported from packages/coding-agent/src/core/tools/find.ts (pi v1.0.0).

package core

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/minimatch"
	"github.com/keejkrej/pi-go/internal/typebox"
	"github.com/keejkrej/pi-go/internal/xspawn"
)

const tfDefaultLimit = 1000

var tfGlobMetaRe = regexp.MustCompile(`[\\*?\[\]{}]`)

// FindToolInput defines parameters for the find tool.
type FindToolInput struct {
	Pattern string   `json:"pattern"`
	Path    *string  `json:"path,omitzero"`
	Limit   *float64 `json:"limit,omitzero"`
}

// FindToolDetails provides truncation and result-limit metadata for rendered find results.
type FindToolDetails struct {
	Truncation         *TruncationResult `json:"truncation,omitzero"`
	ResultLimitReached *int              `json:"resultLimitReached,omitzero"`
}

// FindToolSystemPromptContribution holds the system prompt snippet and guidelines for find.
var FindToolSystemPromptContribution = ToolSystemPromptContribution{
	Snippet:    "Find files by glob pattern (respects .gitignore)",
	Guidelines: []string{},
}

// FindGlobOptions passes ignore globs and limit to FindOperations.Glob.
type FindGlobOptions struct {
	Ignore []string
	Limit  int
}

// FindOperations configures custom filesystem/glob hooks for the find tool.
type FindOperations struct {
	Exists func(ctx context.Context, absolutePath string) (bool, error)
	Glob   func(ctx context.Context, pattern, cwd string, options FindGlobOptions) ([]string, error)
}

// FindToolOptions configures CreateFindToolDefinition and CreateFindTool.
type FindToolOptions struct {
	Operations *FindOperations
}

// RelativizeFindResultPath converts a find match path to a POSIX-separated path relative to searchPath.
func RelativizeFindResultPath(resultPath, searchPath string) string {
	return tfRelativizePath(resultPath, searchPath, runtime.GOOS == "windows")
}

func tfRelativizePath(resultPath, searchPath string, win32 bool) string {
	sep := string(os.PathSeparator)
	if win32 {
		sep = `\`
	}
	hadTrailing := strings.HasSuffix(resultPath, sep) || (win32 && strings.HasSuffix(resultPath, "/"))
	var rel string
	if win32 {
		rel = tfWin32RelativeOrSame(resultPath, searchPath)
		rel = strings.ReplaceAll(rel, `\`, "/")
	} else {
		if filepath.IsAbs(resultPath) {
			if r, err := filepath.Rel(searchPath, resultPath); err == nil {
				rel = r
			} else {
				rel = resultPath
			}
		} else {
			rel = resultPath
		}
		rel = filepath.ToSlash(rel)
	}
	if hadTrailing && !strings.HasSuffix(rel, "/") {
		rel += "/"
	}
	return rel
}

func tfWin32RelativeOrSame(resultPath, searchPath string) string {
	isWinAbs := len(resultPath) >= 2 && (resultPath[1] == ':' || strings.HasPrefix(resultPath, `\\`) || strings.HasPrefix(resultPath, "//"))
	if !isWinAbs && !strings.HasPrefix(resultPath, "/") && !strings.HasPrefix(resultPath, `\`) {
		return resultPath
	}
	normRes := strings.ReplaceAll(resultPath, `/`, `\`)
	normSearch := strings.TrimRight(strings.ReplaceAll(searchPath, `/`, `\`), `\`)
	if len(normRes) >= len(normSearch) && strings.EqualFold(normRes[:len(normSearch)], normSearch) {
		if len(normRes) == len(normSearch) {
			return "."
		}
		if normRes[len(normSearch)] == '\\' {
			return normRes[len(normSearch)+1:]
		}
	}
	if r, err := filepath.Rel(searchPath, resultPath); err == nil {
		return r
	}
	return resultPath
}

func tfFindSchema() *typebox.Schema {
	return tiObjectSchema(
		tiProp{"pattern", typebox.String(jsonx.ObjectOf("description", "Glob pattern relative to the search directory, e.g. 'src/**/*.ts'. Without '/', e.g. '*.ts', it matches file names at any depth"))},
		tiProp{"path", typebox.Optional(typebox.String(jsonx.ObjectOf("description", "Directory to search in (default: current directory)")))},
		tiProp{"limit", typebox.Optional(typebox.Number(jsonx.ObjectOf("description", "Maximum number of results (default: 1000)")))},
	)
}

func tfMatchWindowsPathPattern(pattern, candidate string, ignoreCase bool) bool {
	return minimatch.Match(candidate, pattern, minimatch.Options{
		Dot:    true,
		NoExt:  true,
		NoCase: ignoreCase,
	})
}

func tfInsideGitRepo(ctx context.Context, searchPath string) bool {
	current := searchPath
	for {
		if PathExists(ctx, filepath.Join(current, ".git")) {
			return true
		}
		parent := filepath.Dir(current)
		if parent == current {
			return false
		}
		current = parent
	}
}

// CreateFindToolDefinition creates the ToolDefinition for the built-in find tool.
func CreateFindToolDefinition(cwd string, options ...*FindToolOptions) ToolDefinition {
	var customOps *FindOperations
	if len(options) > 0 && options[0] != nil {
		customOps = options[0].Operations
	}

	snippet := FindToolSystemPromptContribution.Snippet
	def := ToolDefinition{
		Name:          "find",
		Label:         "find",
		Description:   fmt.Sprintf("Search for files by glob pattern. Returns matching file paths relative to the search directory. Respects .gitignore. Output is truncated to %d results or %dKB (whichever is hit first).", tfDefaultLimit, DefaultMaxBytes/1024),
		PromptSnippet: &snippet,
		Parameters:    tfFindSchema(),
		Execute: func(
			ctx context.Context,
			_ string,
			params *jsonx.Object,
			_ agent.AgentToolUpdateCallback,
			ectx ExtensionToolContext,
		) (agent.AgentToolResult, error) {
			if err := tiCheckAborted(ctx); err != nil {
				return agent.AgentToolResult{}, err
			}

			var input FindToolInput
			if params != nil {
				if err := jsonx.Decode(params, &input); err != nil {
					return agent.AgentToolResult{}, err
				}
			}

			searchDir := "."
			if input.Path != nil && *input.Path != "" {
				searchDir = *input.Path
			}
			searchPath := ResolveToCwd(searchDir, tiResolveToolCwd(ectx, cwd))

			effectiveLimit := tfDefaultLimit
			if input.Limit != nil {
				effectiveLimit = max(1, int(math.Floor(*input.Limit)))
			}

			if customOps != nil && customOps.Glob != nil {
				existsFn := customOps.Exists
				if existsFn == nil {
					existsFn = func(c context.Context, p string) (bool, error) {
						return PathExists(c, p), nil
					}
				}
				exists, err := existsFn(ctx, searchPath)
				if err != nil {
					return agent.AgentToolResult{}, err
				}
				if !exists {
					return agent.AgentToolResult{}, fmt.Errorf("Path not found: %s", searchPath)
				}
				if err := tiCheckAborted(ctx); err != nil {
					return agent.AgentToolResult{}, err
				}

				results, err := customOps.Glob(ctx, input.Pattern, searchPath, FindGlobOptions{
					Ignore: []string{"**/node_modules/**", "**/.git/**"},
					Limit:  effectiveLimit + 1,
				})
				if err != nil {
					return agent.AgentToolResult{}, err
				}
				if err := tiCheckAborted(ctx); err != nil {
					return agent.AgentToolResult{}, err
				}
				if len(results) == 0 {
					return agent.AgentToolResult{
						Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "No files found matching pattern"}},
						Details: nil,
					}, nil
				}

				resultLimitReached := len(results) > effectiveLimit
				capped := results
				if resultLimitReached {
					capped = results[:effectiveLimit]
				}
				relativized := make([]string, len(capped))
				for i, p := range capped {
					relativized[i] = RelativizeFindResultPath(p, searchPath)
				}
				truncation := TruncateHead(strings.Join(relativized, "\n"), &TruncationOptions{MaxLines: tlIntPtr(math.MaxInt)})
				return tfFormatFindResult(truncation, resultLimitReached, effectiveLimit, false), nil
			}

			fdPath := utils.EnsureTool(ctx, "fd", nil)
			if err := tiCheckAborted(ctx); err != nil {
				return agent.AgentToolResult{}, err
			}
			if fdPath == nil || *fdPath == "" {
				return agent.AgentToolResult{}, errors.New("fd is not available and could not be downloaded")
			}

			args := []string{"--color=never", "--hidden"}
			if !tfInsideGitRepo(ctx, searchPath) {
				args = append(args, "--no-require-git")
			}

			effectivePattern := input.Pattern
			if runtime.GOOS == "windows" {
				effectivePattern = strings.ReplaceAll(effectivePattern, `\`, "/")
			}
			for strings.HasPrefix(effectivePattern, "./") {
				effectivePattern = effectivePattern[2:]
			}

			isPathPattern := strings.Contains(effectivePattern, "/")
			ignoreCase := effectivePattern == strings.ToLower(effectivePattern)
			isRelativePathPattern := isPathPattern && !filepath.IsAbs(effectivePattern)
			useWindowsMatcher := runtime.GOOS == "windows" && isPathPattern
			winPattern := ""
			if useWindowsMatcher {
				winPattern = strings.TrimSuffix(effectivePattern, "/**")
				if winPattern != effectivePattern {
					winPattern += "/**/*"
				}
				args = append(args, "--", "", searchPath)
			} else {
				args = append(args, "--glob", "--max-results", strconv.Itoa(effectiveLimit+1))
				if isPathPattern {
					args = append(args, "--full-path")
				}
				if isRelativePathPattern {
					trimmedRoot := strings.TrimRight(searchPath, "/")
					escapedRoot := tfGlobMetaRe.ReplaceAllString(trimmedRoot, `\$0`)
					effectivePattern = escapedRoot + "/" + effectivePattern
					if ignoreCase {
						args = append(args, "--ignore-case")
					}
				}
				args = append(args, "--", effectivePattern, searchPath)
			}

			if err := tiCheckAborted(ctx); err != nil {
				return agent.AgentToolResult{}, err
			}

			cmd := utils.SpawnProcessIn(context.Background(), "", nil, *fdPath, args)
			stdoutPipe, err := cmd.StdoutPipe()
			if err != nil {
				return agent.AgentToolResult{}, fmt.Errorf("Failed to run fd: %w", err)
			}
			stderrPipe, err := cmd.StderrPipe()
			if err != nil {
				return agent.AgentToolResult{}, fmt.Errorf("Failed to run fd: %w", err)
			}

			if err := cmd.Start(); err != nil {
				err = xspawn.VerifyENOENT(cmd, err)
				return agent.AgentToolResult{}, fmt.Errorf("Failed to run fd: %w", err)
			}

			var (
				stopOnce sync.Once
				stderr   strings.Builder
			)
			stopChild := func() {
				stopOnce.Do(func() {
					if cmd.Process != nil {
						_ = cmd.Process.Kill()
					}
				})
			}

			stopWatch := make(chan struct{})
			if ctx != nil && ctx.Done() != nil {
				go func() {
					select {
					case <-ctx.Done():
						stopChild()
					case <-stopWatch:
					}
				}()
			}

			var errWg sync.WaitGroup
			errWg.Add(1)
			go func() {
				defer errWg.Done()
				buf := make([]byte, 16*1024)
				for {
					n, rErr := stderrPipe.Read(buf)
					if n > 0 {
						stderr.Write(buf[:n])
					}
					if rErr != nil {
						return
					}
				}
			}()

			var storedResults []string
			resultCount := 0
			outputBytes := 0
			resultLimitReached := false

			reader := bufio.NewReaderSize(stdoutPipe, 64*1024)
			for {
				lineBytes, readErr := reader.ReadBytes('\n')
				if len(lineBytes) > 0 {
					rawLine := strings.TrimSpace(strings.TrimRight(string(lineBytes), "\r\n"))
					if rawLine != "" {
						rel := RelativizeFindResultPath(rawLine, searchPath)
						matched := true
						if useWindowsMatcher {
							target := rawLine
							if isRelativePathPattern {
								target = rel
							}
							matched = tfMatchWindowsPathPattern(winPattern, target, ignoreCase)
						}
						if matched {
							if resultCount == effectiveLimit {
								resultLimitReached = true
								if useWindowsMatcher {
									stopChild()
								}
							} else {
								resultCount++
								if outputBytes-1 <= DefaultMaxBytes {
									storedResults = append(storedResults, rel)
								}
								outputBytes += len(rel) + 1
							}
						}
					}
				}
				if readErr != nil {
					if errors.Is(readErr, io.EOF) {
						break
					}
					break
				}
			}

			errWg.Wait()
			waitErr := cmd.Wait()
			close(stopWatch)

			if ctx != nil && ctx.Err() != nil {
				return agent.AgentToolResult{}, errors.New("Operation aborted")
			}

			exitCode := 0
			if waitErr != nil {
				if cmd.ProcessState != nil {
					exitCode = cmd.ProcessState.ExitCode()
				} else {
					return agent.AgentToolResult{}, fmt.Errorf("Failed to run fd: %w", waitErr)
				}
			}

			if exitCode != 0 && resultCount == 0 {
				msg := strings.TrimSpace(stderr.String())
				if msg == "" {
					msg = fmt.Sprintf("fd exited with code %d", exitCode)
				}
				return agent.AgentToolResult{}, errors.New(msg)
			}

			if resultCount == 0 {
				return agent.AgentToolResult{
					Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "No files found matching pattern"}},
					Details: nil,
				}, nil
			}

			truncation := TruncateHead(strings.Join(storedResults, "\n"), &TruncationOptions{MaxLines: tlIntPtr(math.MaxInt)})
			truncation.TotalLines = resultCount
			truncation.TotalBytes = outputBytes - 1
			return tfFormatFindResult(truncation, resultLimitReached, effectiveLimit, true), nil
		},
	}
	tiApplyToolRenderers(&def, FindToolRenderers())
	return def
}

func tfFormatFindResult(truncation TruncationResult, resultLimitReached bool, effectiveLimit int, includeLimitHint bool) agent.AgentToolResult {
	output := truncation.Content
	var details FindToolDetails
	hasDetails := false
	var notices []string
	if resultLimitReached {
		if includeLimitHint {
			notices = append(notices, fmt.Sprintf("%d results limit reached. Use limit=%d for more, or refine pattern", effectiveLimit, effectiveLimit*2))
		} else {
			notices = append(notices, fmt.Sprintf("%d results limit reached", effectiveLimit))
		}
		limitCopy := effectiveLimit
		details.ResultLimitReached = &limitCopy
		hasDetails = true
	}
	if truncation.Truncated {
		notices = append(notices, fmt.Sprintf("%s limit reached", FormatSize(DefaultMaxBytes)))
		tCopy := truncation
		details.Truncation = &tCopy
		hasDetails = true
	}
	if len(notices) > 0 {
		output += "\n\n[" + strings.Join(notices, ". ") + "]"
	}
	var detailsVal any
	if hasDetails {
		detailsVal = &details
	}
	return agent.AgentToolResult{
		Content: ai.ContentList{&ai.TextContent{Type: "text", Text: output}},
		Details: detailsVal,
	}
}

// CreateFindTool wraps CreateFindToolDefinition as an executable *agent.AgentTool.
func CreateFindTool(cwd string, options ...*FindToolOptions) *agent.AgentTool {
	return WrapToolDefinition(CreateFindToolDefinition(cwd, options...))
}
