// Ported from packages/coding-agent/src/core/tools/write.ts (pi v1.0.0).

package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/typebox"
)

// WriteToolInput defines parameters for the write tool.
type WriteToolInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// WriteToolSystemPromptContribution holds the system prompt snippet and guidelines for write.
var WriteToolSystemPromptContribution = ToolSystemPromptContribution{
	Snippet:    "Create or overwrite files",
	Guidelines: []string{"Use write only for new files or complete rewrites."},
}

// WriteOperations configures filesystem hooks for the write tool.
type WriteOperations struct {
	WriteFile func(ctx context.Context, absolutePath, content string) error
	Mkdir     func(ctx context.Context, dir string) error
}

// WriteToolOptions configures CreateWriteToolDefinition and CreateWriteTool.
type WriteToolOptions struct {
	Operations *WriteOperations
}

func twDefaultWriteOperations() WriteOperations {
	return WriteOperations{
		WriteFile: func(_ context.Context, absolutePath, content string) error {
			return os.WriteFile(absolutePath, []byte(content), 0o666)
		},
		Mkdir: func(_ context.Context, dir string) error {
			return os.MkdirAll(dir, 0o777)
		},
	}
}

func twWriteSchema() *typebox.Schema {
	return tiObjectSchema(
		tiProp{"path", typebox.String(jsonx.ObjectOf("description", "Path to the file to write (relative or absolute)"))},
		tiProp{"content", typebox.String(jsonx.ObjectOf("description", "Content to write to the file"))},
	)
}

// CreateWriteToolDefinition creates the ToolDefinition for the built-in write tool.
func CreateWriteToolDefinition(cwd string, options ...*WriteToolOptions) ToolDefinition {
	ops := twDefaultWriteOperations()
	if len(options) > 0 && options[0] != nil && options[0].Operations != nil {
		ops = *options[0].Operations
	}

	snippet := WriteToolSystemPromptContribution.Snippet
	def := ToolDefinition{
		Name:                "write",
		Label:               "write",
		Description:         "Write content to a file. Creates the file if it doesn't exist, overwrites if it does. Automatically creates parent directories.",
		PromptSnippet:       &snippet,
		PromptGuidelines:    slices.Clone(WriteToolSystemPromptContribution.Guidelines),
		Parameters:          twWriteSchema(),
		ConstrainedSampling: tiStrictPreferJsonSchema(),
		Execute: func(
			ctx context.Context,
			_ string,
			params *jsonx.Object,
			_ agent.AgentToolUpdateCallback,
			ectx ExtensionToolContext,
		) (agent.AgentToolResult, error) {
			var input WriteToolInput
			if params != nil {
				if err := jsonx.Decode(params, &input); err != nil {
					return agent.AgentToolResult{}, err
				}
			}

			effectiveCwd := tiResolveToolCwd(ectx, cwd)
			absolutePath := ResolveToCwd(input.Path, effectiveCwd)
			dir := filepath.Dir(absolutePath)

			return WithFileMutationQueue(ctx, absolutePath, func() (agent.AgentToolResult, error) {
				if err := tiCheckAborted(ctx); err != nil {
					return agent.AgentToolResult{}, err
				}
				if ops.Mkdir != nil {
					if err := ops.Mkdir(ctx, dir); err != nil {
						return agent.AgentToolResult{}, err
					}
				}
				if err := tiCheckAborted(ctx); err != nil {
					return agent.AgentToolResult{}, err
				}
				if ops.WriteFile == nil {
					return agent.AgentToolResult{}, errors.New("writeFile operation is not configured")
				}
				if err := ops.WriteFile(ctx, absolutePath, input.Content); err != nil {
					return agent.AgentToolResult{}, err
				}
				if err := tiCheckAborted(ctx); err != nil {
					return agent.AgentToolResult{}, err
				}

				return agent.AgentToolResult{
					Content: ai.ContentList{
						&ai.TextContent{
							Type: "text",
							Text: fmt.Sprintf("Successfully wrote to %s", input.Path),
						},
					},
					Details: nil,
				}, nil
			})
		},
	}
	tiApplyToolRenderers(&def, WriteToolRenderers())
	return def
}

// CreateWriteTool wraps CreateWriteToolDefinition as an executable *agent.AgentTool.
func CreateWriteTool(cwd string, options ...*WriteToolOptions) *agent.AgentTool {
	return WrapToolDefinition(CreateWriteToolDefinition(cwd, options...))
}
