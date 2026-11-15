// Ported from packages/coding-agent/examples/extensions/dynamic-tools.ts (pi v1.0.0).

package dynamictools

import (
	"context"
	"regexp"
	"strings"
	"sync"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

var validToolNameRe = regexp.MustCompile(`^[a-z0-9_]+$`)

// Extension registers echo_session on session_start and adds tools dynamically via /add-echo-tool.
func Extension(pi core.ExtensionAPI) error {
	var mu sync.Mutex
	registered := make(map[string]struct{})

	registerEcho := func(name, label, prefix string) bool {
		mu.Lock()
		if _, exists := registered[name]; exists {
			mu.Unlock()
			return false
		}
		registered[name] = struct{}{}
		mu.Unlock()

		props := omap.NewMap[string, *typebox.Schema]()
		props.Set("message", typebox.String(jsonx.ObjectOf("description", "Message to echo")))
		pi.RegisterTool(core.ToolDefinition{
			Name:        name,
			Label:       label,
			Description: "Echo a message with prefix: " + prefix,
			Parameters:  typebox.Object(props),
			Execute: func(_ context.Context, _ string, params *jsonx.Object, _ agent.AgentToolUpdateCallback, _ core.ExtensionToolContext) (agent.AgentToolResult, error) {
				msg, _ := params.GetString("message")
				return agent.AgentToolResult{
					Content: ai.ContentList{&ai.TextContent{Type: "text", Text: prefix + msg}},
					Details: map[string]any{"tool": name, "prefix": prefix},
				}, nil
			},
		})
		return true
	}

	pi.OnSessionStart(func(_ context.Context, _ *core.SessionStartEvent, ectx core.ExtensionContext) (*core.NoResult, error) {
		registerEcho("echo_session", "Echo Session", "[session] ")
		ectx.UI().Notify("Registered dynamic tool: echo_session", "info")
		return nil, nil
	})

	desc := "Register a new echo tool dynamically: /add-echo-tool <tool_name>"
	pi.RegisterCommand("add-echo-tool", core.RegisteredCommand{
		Description: &desc,
		Handler: func(_ context.Context, args string, ectx core.ExtensionCommandContext) error {
			name := strings.ToLower(strings.TrimSpace(args))
			if !validToolNameRe.MatchString(name) {
				ectx.UI().Notify("Usage: /add-echo-tool <tool_name>", "warning")
				return nil
			}
			registerEcho(name, "Echo "+name, "["+name+"] ")
			return nil
		},
	})
	return nil
}
