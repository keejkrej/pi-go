// Ported from packages/coding-agent/src/core/tools/grep.ts (pi v1.0.0).

package core

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/typebox"
	"github.com/keejkrej/pi-go/internal/xspawn"
)

const tgDefaultLimit = 100

// GrepToolInput defines parameters for the grep tool.
type GrepToolInput struct {
	Pattern    string   `json:"pattern"`
	Path       *string  `json:"path,omitzero"`
	Glob       *string  `json:"glob,omitzero"`
	IgnoreCase *bool    `json:"ignoreCase,omitzero"`
	Literal    *bool    `json:"literal,omitzero"`
	Context    *float64 `json:"context,omitzero"`
	Limit      *float64 `json:"limit,omitzero"`
}

// GrepToolDetails provides truncation and match-limit metadata for rendered grep results.
type GrepToolDetails struct {
	Truncation        *TruncationResult `json:"truncation,omitzero"`
	MatchLimitReached *int              `json:"matchLimitReached,omitzero"`
	LinesTruncated    *bool             `json:"linesTruncated,omitzero"`
}

// GrepToolSystemPromptContribution holds the system prompt snippet and guidelines for grep.
var GrepToolSystemPromptContribution = ToolSystemPromptContribution{
	Snippet:    "Search file contents for patterns (respects .gitignore)",
	Guidelines: []string{},
}

// GrepOperations configures path directory detection for the grep tool.
type GrepOperations struct {
	IsDirectory func(ctx context.Context, absolutePath string) (bool, error)
	ReadFile    func(ctx context.Context, absolutePath string) (string, error)
}

// GrepToolOptions configures CreateGrepToolDefinition and CreateGrepTool.
type GrepToolOptions struct {
	Operations *GrepOperations
}

type tgRgTextField struct {
	Text  *string `json:"text,omitzero"`
	Bytes *string `json:"bytes,omitzero"`
}

type tgRgEvent struct {
	Type string `json:"type"`
	Data struct {
		Path       *tgRgTextField `json:"path,omitzero"`
		LineNumber *int           `json:"line_number,omitzero"`
		Lines      *tgRgTextField `json:"lines,omitzero"`
	} `json:"data"`
}

func tgDefaultGrepOperations() GrepOperations {
	return GrepOperations{
		IsDirectory: func(_ context.Context, absolutePath string) (bool, error) {
			info, err := os.Stat(absolutePath)
			if err != nil {
				return false, err
			}
			return info.IsDir(), nil
		},
	}
}

func tgDecodeRgText(field *tgRgTextField) string {
	if field == nil {
		return ""
	}
	if field.Text != nil {
		return *field.Text
	}
	if field.Bytes != nil {
		raw, err := base64.StdEncoding.DecodeString(*field.Bytes)
		if err != nil {
			return ""
		}
		return js.NewUTF8StreamDecoder().DecodeFinal(raw)
	}
	return ""
}

func tgCleanRgGlob(glob string) string {
	negated := false
	rest := glob
	if strings.HasPrefix(rest, "!") {
		negated = true
		rest = rest[1:]
	}
	for strings.HasPrefix(rest, "./") {
		rest = rest[2:]
	}
	if negated {
		return "!" + rest
	}
	return rest
}

func tgGrepSchema() *typebox.Schema {
	return tiObjectSchema(
		tiProp{"pattern", typebox.String(jsonx.ObjectOf("description", "Search pattern (regex or literal string)"))},
		tiProp{"path", typebox.Optional(typebox.String(jsonx.ObjectOf("description", "Directory or file to search (default: current directory)")))},
		tiProp{"glob", typebox.Optional(typebox.String(jsonx.ObjectOf("description", "Filter files by glob pattern relative to path, e.g. 'src/**/*.ts'. Without '/', e.g. '*.ts', it matches file names at any depth")))},
		tiProp{"ignoreCase", typebox.Optional(typebox.Boolean(jsonx.ObjectOf("description", "Case-insensitive search (default: false)")))},
		tiProp{"literal", typebox.Optional(typebox.Boolean(jsonx.ObjectOf("description", "Treat pattern as literal string instead of regex (default: false)")))},
		tiProp{"context", typebox.Optional(typebox.Number(jsonx.ObjectOf("description", "Number of lines to show before and after each match (default: 0)")))},
		tiProp{"limit", typebox.Optional(typebox.Number(jsonx.ObjectOf("description", "Maximum number of matches to return (default: 100)")))},
	)
}

// CreateGrepToolDefinition creates the ToolDefinition for the built-in grep tool.
func CreateGrepToolDefinition(cwd string, options ...*GrepToolOptions) ToolDefinition {
	var customOps *GrepOperations
	if len(options) > 0 && options[0] != nil {
		customOps = options[0].Operations
	}

	snippet := GrepToolSystemPromptContribution.Snippet
	def := ToolDefinition{
		Name:          "grep",
		Label:         "grep",
		Description:   fmt.Sprintf("Search file contents for a pattern. Returns matching lines with file paths and line numbers. Respects .gitignore. Output is truncated to %d matches or %dKB (whichever is hit first). Long lines are truncated to %d chars.", tgDefaultLimit, DefaultMaxBytes/1024, GrepMaxLineLength),
		PromptSnippet: &snippet,
		Parameters:    tgGrepSchema(),
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

			var input GrepToolInput
			if params != nil {
				if err := jsonx.Decode(params, &input); err != nil {
					return agent.AgentToolResult{}, err
				}
			}

			rgPath := utils.EnsureTool(ctx, "rg", nil)
			if err := tiCheckAborted(ctx); err != nil {
				return agent.AgentToolResult{}, err
			}
			if rgPath == nil || *rgPath == "" {
				return agent.AgentToolResult{}, errors.New("ripgrep (rg) is not available and could not be downloaded")
			}

			searchDir := "."
			if input.Path != nil && *input.Path != "" {
				searchDir = *input.Path
			}
			searchPath := ResolveToCwd(searchDir, tiResolveToolCwd(ectx, cwd))

			ops := tgDefaultGrepOperations()
			if customOps != nil {
				ops = *customOps
			}
			if ops.IsDirectory == nil {
				return agent.AgentToolResult{}, errors.New("isDirectory operation is not configured")
			}
			isDirectory, err := ops.IsDirectory(ctx, searchPath)
			if err != nil {
				return agent.AgentToolResult{}, fmt.Errorf("Path not found: %s", searchPath)
			}

			contextValue := 0
			if input.Context != nil && *input.Context > 0 {
				contextValue = int(math.Floor(*input.Context))
			}
			effectiveLimit := tgDefaultLimit
			if input.Limit != nil {
				effectiveLimit = max(1, int(math.Floor(*input.Limit)))
			}

			formatPath := func(filePath string) string {
				if isDirectory {
					resolvedFile := filePath
					if !filepath.IsAbs(resolvedFile) {
						resolvedFile = filepath.Join(searchPath, filePath)
					}
					if rel, err := filepath.Rel(searchPath, resolvedFile); err == nil && rel != "" && !strings.HasPrefix(rel, "..") {
						return strings.ReplaceAll(rel, `\`, "/")
					}
				}
				return filepath.Base(filePath)
			}

			args := []string{"--no-config", "--json", "--line-number", "--color=never", "--hidden"}
			if input.IgnoreCase != nil && *input.IgnoreCase {
				args = append(args, "--ignore-case")
			}
			if input.Literal != nil && *input.Literal {
				args = append(args, "--fixed-strings")
			}
			if input.Glob != nil && *input.Glob != "" {
				args = append(args, "--glob", tgCleanRgGlob(*input.Glob))
			}
			if contextValue > 0 {
				args = append(args, "--context", strconv.Itoa(contextValue))
			}
			target := searchPath
			cmdDir := ""
			if isDirectory {
				target = "."
				cmdDir = searchPath
			}
			args = append(args, "--", input.Pattern, target)

			if err := tiCheckAborted(ctx); err != nil {
				return agent.AgentToolResult{}, err
			}

			cmd := utils.SpawnProcessIn(context.Background(), cmdDir, nil, *rgPath, args)
			stdoutPipe, err := cmd.StdoutPipe()
			if err != nil {
				return agent.AgentToolResult{}, fmt.Errorf("Failed to run ripgrep: %w", err)
			}
			stderrPipe, err := cmd.StderrPipe()
			if err != nil {
				return agent.AgentToolResult{}, fmt.Errorf("Failed to run ripgrep: %w", err)
			}

			if err := cmd.Start(); err != nil {
				err = xspawn.VerifyENOENT(cmd, err)
				return agent.AgentToolResult{}, fmt.Errorf("Failed to run ripgrep: %w", err)
			}

			var (
				mu               sync.Mutex
				aborted          bool
				killedDueToLimit bool
				stderr           strings.Builder
			)
			stopChild := func(dueToLimit bool) {
				mu.Lock()
				if dueToLimit {
					killedDueToLimit = true
				}
				mu.Unlock()
				if cmd.Process != nil {
					_ = cmd.Process.Kill()
				}
			}

			stopWatch := make(chan struct{})
			if ctx != nil && ctx.Done() != nil {
				go func() {
					select {
					case <-ctx.Done():
						mu.Lock()
						aborted = true
						mu.Unlock()
						stopChild(false)
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

			matchCount := 0
			matchLimitReached := false
			linesTruncated := false
			var outputLines []string
			outputBytes := 0
			outputLineCount := 0
			lastFile := ""
			hasLastFile := false
			limitHit := false
			trailingContextEnd := 0
			done := false

			finish := func() {
				done = true
				stopChild(true)
			}

			pushLine := func(filePath string, lineNumber int, lineText string, isMatch bool) {
				lastFile = filePath
				hasLastFile = true
				relativePath := formatPath(filePath)
				sanitized := strings.TrimSuffix(lineText, "\n")
				sanitized = strings.TrimSuffix(sanitized, "\r")
				trunc := TruncateLine(sanitized)
				if trunc.WasTruncated {
					linesTruncated = true
				}
				var line string
				if isMatch {
					line = fmt.Sprintf("%s:%d: %s", relativePath, lineNumber, trunc.Text)
				} else {
					line = fmt.Sprintf("%s-%d- %s", relativePath, lineNumber, trunc.Text)
				}
				if outputBytes-1 <= DefaultMaxBytes {
					outputLines = append(outputLines, line)
				}
				outputBytes += len(line) + 1
				outputLineCount++
			}

			reader := bufio.NewReaderSize(stdoutPipe, 64*1024)
			for !done {
				rawLine, readErr := reader.ReadBytes('\n')
				if len(rawLine) > 0 && !done {
					trimmed := strings.TrimSpace(string(rawLine))
					if trimmed != "" {
						var ev tgRgEvent
						if uErr := jsonx.Unmarshal([]byte(trimmed), &ev); uErr == nil {
							isMatch := ev.Type == "match"
							if isMatch || ev.Type == "context" {
								filePath := tgDecodeRgText(ev.Data.Path)
								if filePath != "" && ev.Data.LineNumber != nil {
									lineNumber := *ev.Data.LineNumber
									lineText := tgDecodeRgText(ev.Data.Lines)
									if !limitHit {
										pushLine(filePath, lineNumber, lineText, isMatch)
										if isMatch {
											matchCount++
											if matchCount >= effectiveLimit {
												limitHit = true
												trailingContextEnd = lineNumber + contextValue
											}
										}
									} else {
										inWindow := hasLastFile && filePath == lastFile && lineNumber <= trailingContextEnd
										if inWindow {
											pushLine(filePath, lineNumber, lineText, false)
										}
										if isMatch {
											matchLimitReached = true
										}
										if matchLimitReached && !(inWindow && lineNumber < trailingContextEnd) {
											finish()
										}
									}
								}
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
			_ = stdoutPipe.Close()

			errWg.Wait()
			waitErr := cmd.Wait()
			close(stopWatch)

			mu.Lock()
			wasAborted := aborted || (ctx != nil && ctx.Err() != nil)
			wasKilledDueToLimit := killedDueToLimit
			mu.Unlock()

			if wasAborted {
				return agent.AgentToolResult{}, errors.New("Operation aborted")
			}

			exitCode := 0
			if waitErr != nil && cmd.ProcessState != nil {
				exitCode = cmd.ProcessState.ExitCode()
			} else if waitErr != nil && !wasKilledDueToLimit {
				return agent.AgentToolResult{}, fmt.Errorf("Failed to run ripgrep: %w", waitErr)
			}

			if !wasKilledDueToLimit && exitCode != 0 && exitCode != 1 {
				msg := strings.TrimSpace(stderr.String())
				if msg == "" {
					msg = fmt.Sprintf("ripgrep exited with code %d", exitCode)
				}
				return agent.AgentToolResult{}, errors.New(msg)
			}

			if matchCount == 0 {
				return agent.AgentToolResult{
					Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "No matches found"}},
					Details: nil,
				}, nil
			}

			truncation := TruncateHead(strings.Join(outputLines, "\n"), &TruncationOptions{MaxLines: tlIntPtr(math.MaxInt)})
			truncation.TotalLines = outputLineCount
			truncation.TotalBytes = outputBytes - 1

			output := truncation.Content
			var details GrepToolDetails
			hasDetails := false
			var notices []string
			if matchLimitReached {
				notices = append(notices, fmt.Sprintf("%d matches limit reached. Use limit=%d for more, or refine pattern", effectiveLimit, effectiveLimit*2))
				limitCopy := effectiveLimit
				details.MatchLimitReached = &limitCopy
				hasDetails = true
			}
			if truncation.Truncated {
				notices = append(notices, fmt.Sprintf("%s limit reached", FormatSize(DefaultMaxBytes)))
				tCopy := truncation
				details.Truncation = &tCopy
				hasDetails = true
			}
			if linesTruncated {
				notices = append(notices, fmt.Sprintf("Some lines truncated to %d chars. Use read tool to see full lines", GrepMaxLineLength))
				lt := true
				details.LinesTruncated = &lt
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
			}, nil
		},
	}
	tiApplyToolRenderers(&def, GrepToolRenderers())
	return def
}

// CreateGrepTool wraps CreateGrepToolDefinition as an executable *agent.AgentTool.
func CreateGrepTool(cwd string, options ...*GrepToolOptions) *agent.AgentTool {
	return WrapToolDefinition(CreateGrepToolDefinition(cwd, options...))
}
