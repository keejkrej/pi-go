// Ported from packages/coding-agent/src/core/tools/ls.ts (pi v1.0.0).

package core

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/typebox"
)

const tlDefaultLimit = 500

// LsToolInput defines parameters for the ls tool.
type LsToolInput struct {
	Path  *string  `json:"path,omitzero"`
	Limit *float64 `json:"limit,omitzero"`
}

// LsToolDetails provides truncation and entry-limit metadata for rendered ls results.
type LsToolDetails struct {
	Truncation        *TruncationResult `json:"truncation,omitzero"`
	EntryLimitReached *int              `json:"entryLimitReached,omitzero"`
}

// LsToolSystemPromptContribution holds the system prompt snippet and guidelines for ls.
var LsToolSystemPromptContribution = ToolSystemPromptContribution{
	Snippet:    "List directory contents",
	Guidelines: []string{},
}

// LsFileInfo is the minimal file stat interface consumed by LsOperations.Stat (satisfied by os.FileInfo).
type LsFileInfo interface {
	IsDir() bool
}

// LsOperations configures filesystem hooks for the ls tool.
type LsOperations struct {
	Exists  func(ctx context.Context, absolutePath string) (bool, error)
	Stat    func(ctx context.Context, absolutePath string) (LsFileInfo, error)
	Readdir func(ctx context.Context, absolutePath string) ([]string, error)
}

// LsToolOptions configures CreateLsToolDefinition and CreateLsTool.
type LsToolOptions struct {
	Operations *LsOperations
}

func tlDefaultLsOperations() LsOperations {
	return LsOperations{
		Exists: func(ctx context.Context, absolutePath string) (bool, error) {
			return PathExists(ctx, absolutePath), nil
		},
		Stat: func(_ context.Context, absolutePath string) (LsFileInfo, error) {
			return os.Stat(absolutePath)
		},
		Readdir: func(_ context.Context, absolutePath string) ([]string, error) {
			entries, err := os.ReadDir(absolutePath)
			if err != nil {
				return nil, err
			}
			names := make([]string, len(entries))
			for i, entry := range entries {
				names[i] = entry.Name()
			}
			return names, nil
		},
	}
}

func tlLsSchema() *typebox.Schema {
	return tiObjectSchema(
		tiProp{"path", typebox.Optional(typebox.String(jsonx.ObjectOf("description", "Directory to list (default: current directory)")))},
		tiProp{"limit", typebox.Optional(typebox.Number(jsonx.ObjectOf("description", "Maximum number of entries to return (default: 500)")))},
	)
}

// CreateLsToolDefinition creates the ToolDefinition for the built-in ls tool.
func CreateLsToolDefinition(cwd string, options ...*LsToolOptions) ToolDefinition {
	ops := tlDefaultLsOperations()
	if len(options) > 0 && options[0] != nil && options[0].Operations != nil {
		ops = *options[0].Operations
	}

	snippet := LsToolSystemPromptContribution.Snippet
	def := ToolDefinition{
		Name:          "ls",
		Label:         "ls",
		Description:   fmt.Sprintf("List directory contents. Returns entries sorted alphabetically, with '/' suffix for directories. Includes dotfiles. Output is truncated to %d entries or %dKB (whichever is hit first).", tlDefaultLimit, DefaultMaxBytes/1024),
		PromptSnippet: &snippet,
		Parameters:    tlLsSchema(),
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

			var input LsToolInput
			if params != nil {
				if err := jsonx.Decode(params, &input); err != nil {
					return agent.AgentToolResult{}, err
				}
			}

			relPath := "."
			if input.Path != nil && *input.Path != "" {
				relPath = *input.Path
			}
			dirPath := ResolveToCwd(relPath, tiResolveToolCwd(ectx, cwd))

			effectiveLimit := tlDefaultLimit
			if input.Limit != nil {
				effectiveLimit = max(1, int(math.Floor(*input.Limit)))
			}

			if ops.Exists != nil {
				exists, err := ops.Exists(ctx, dirPath)
				if err != nil {
					return agent.AgentToolResult{}, err
				}
				if !exists {
					return agent.AgentToolResult{}, fmt.Errorf("Path not found: %s", dirPath)
				}
			}
			if err := tiCheckAborted(ctx); err != nil {
				return agent.AgentToolResult{}, err
			}

			if ops.Stat == nil {
				return agent.AgentToolResult{}, errors.New("stat operation is not configured")
			}
			statInfo, err := ops.Stat(ctx, dirPath)
			if err != nil {
				return agent.AgentToolResult{}, err
			}
			if !statInfo.IsDir() {
				return agent.AgentToolResult{}, fmt.Errorf("Not a directory: %s", dirPath)
			}
			if err := tiCheckAborted(ctx); err != nil {
				return agent.AgentToolResult{}, err
			}

			if ops.Readdir == nil {
				return agent.AgentToolResult{}, errors.New("readdir operation is not configured")
			}
			entries, err := ops.Readdir(ctx, dirPath)
			if err != nil {
				return agent.AgentToolResult{}, fmt.Errorf("Cannot read directory: %s", err.Error())
			}
			if err := tiCheckAborted(ctx); err != nil {
				return agent.AgentToolResult{}, err
			}

			sorted := slices.Clone(entries)
			slices.SortStableFunc(sorted, func(a, b string) int {
				return js.LocaleCompare(strings.ToLower(a), strings.ToLower(b))
			})

			var results []string
			entryLimitReached := false
			for _, entry := range sorted {
				if err := tiCheckAborted(ctx); err != nil {
					return agent.AgentToolResult{}, err
				}
				if len(results) >= effectiveLimit {
					entryLimitReached = true
					break
				}
				fullPath := filepath.Join(dirPath, entry)
				entryStat, err := ops.Stat(ctx, fullPath)
				if err != nil {
					continue
				}
				suffix := ""
				if entryStat.IsDir() {
					suffix = "/"
				}
				results = append(results, entry+suffix)
			}

			if err := tiCheckAborted(ctx); err != nil {
				return agent.AgentToolResult{}, err
			}

			if len(results) == 0 {
				return agent.AgentToolResult{
					Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "(empty directory)"}},
					Details: nil,
				}, nil
			}

			rawOutput := strings.Join(results, "\n")
			truncation := TruncateHead(rawOutput, &TruncationOptions{MaxLines: tlIntPtr(math.MaxInt)})
			output := truncation.Content
			var details LsToolDetails
			hasDetails := false
			var notices []string
			if entryLimitReached {
				notices = append(notices, fmt.Sprintf("%d entries limit reached. Use limit=%d for more", effectiveLimit, effectiveLimit*2))
				limitCopy := effectiveLimit
				details.EntryLimitReached = &limitCopy
				hasDetails = true
			}
			if truncation.Truncated {
				notices = append(notices, fmt.Sprintf("%s limit reached", FormatSize(DefaultMaxBytes)))
				truncCopy := truncation
				details.Truncation = &truncCopy
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
	tiApplyToolRenderers(&def, LsToolRenderers())
	return def
}

func tlIntPtr(v int) *int { return &v }

// CreateLsTool wraps CreateLsToolDefinition as an executable *agent.AgentTool.
func CreateLsTool(cwd string, options ...*LsToolOptions) *agent.AgentTool {
	return WrapToolDefinition(CreateLsToolDefinition(cwd, options...))
}
