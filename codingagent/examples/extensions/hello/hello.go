// Ported from packages/coding-agent/examples/extensions/hello.ts (pi v1.0.0).

package hello

import (
	"context"
	"fmt"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

// Extension registers the hello greeting tool.
func Extension(pi core.ExtensionAPI) error {
	props := omap.NewMap[string, *typebox.Schema]()
	props.Set("name", typebox.String(jsonx.ObjectOf("description", "Name to greet")))
	pi.RegisterTool(core.ToolDefinition{
		Name:        "hello",
		Label:       "Hello",
		Description: "A simple greeting tool",
		Parameters:  typebox.Object(props),
		Execute: func(_ context.Context, _ string, params *jsonx.Object, _ agent.AgentToolUpdateCallback, _ core.ExtensionToolContext) (agent.AgentToolResult, error) {
			name, _ := params.GetString("name")
			return agent.AgentToolResult{
				Content: ai.ContentList{&ai.TextContent{Type: "text", Text: fmt.Sprintf("Hello, %s!", name)}},
				Details: map[string]any{"greeted": name},
			}, nil
		},
	})
	return nil
}
