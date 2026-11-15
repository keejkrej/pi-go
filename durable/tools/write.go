// Ported from packages/durable/src/tools/write.ts (pi v1.0.0).

package tools

import (
	"errors"
	"fmt"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/env"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/typebox"
)

// WriteToolInput defines arguments accepted by the built-in write tool.
type WriteToolInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func twrtWriteSchema() *typebox.Schema {
	return tidxObjectSchema(
		tidxProp{"path", typebox.String(jsonx.ObjectOf("description", "Path to the file to write (relative or absolute)"))},
		tidxProp{"content", typebox.String(jsonx.ObjectOf("description", "Content to write to the file"))},
	)
}

// CreateWriteTool constructs the durable "write" tool registration.
func CreateWriteTool() *durable.ToolRegistration {
	return durable.DefineTool(&durable.ToolRegistration{
		Name:        "write",
		Description: "Write content to a file. Creates the file if it doesn't exist, overwrites if it does. Automatically creates parent directories.",
		Parameters:  twrtWriteSchema(),
		Execute: func(args any, api durable.ToolExecutionApi, ctx chord.Context) (*durable.ToolExecutionResult, error) {
			input, err := tidxDecodeArgs[WriteToolInput](args)
			if err != nil {
				return nil, err
			}
			executionEnv, err := RequireEnv(api, ctx)
			if err != nil {
				return nil, err
			}
			absolutePath, err := ResolveToolPath(executionEnv, input.Path, ctx)
			if err != nil {
				return nil, err
			}
			return WithFileMutationQueue(executionEnv, absolutePath, func() (*durable.ToolExecutionResult, error) {
				if tidxIsAborted(ctx) {
					return nil, errors.New("Operation aborted")
				}
				if _, err := env.GetOrThrow(executionEnv.WriteFile(absolutePath, []byte(input.Content), ctx)); err != nil {
					return nil, err
				}
				if tidxIsAborted(ctx) {
					return nil, errors.New("Operation aborted")
				}
				return &durable.ToolExecutionResult{
					Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: fmt.Sprintf("Successfully wrote to %s", input.Path)}},
					HasContent: true,
				}, nil
			}, ctx)
		},
	})
}
