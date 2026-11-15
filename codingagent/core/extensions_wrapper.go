// Ported from packages/coding-agent/src/core/extensions/wrapper.ts (pi v1.0.0).

package core

import (
	"context"

	"github.com/keejkrej/pi-go/agent"
)

// WrapRegisteredTool wraps a RegisteredTool into an *agent.AgentTool bound to runner.CreateToolContext.
func WrapRegisteredTool(registeredTool RegisteredTool, runner *ExtensionRunner) *agent.AgentTool {
	return WrapToolDefinition(registeredTool.Definition, func(ctx context.Context, toolCallId string) ExtensionToolContext {
		if runner == nil {
			return nil
		}
		return runner.CreateToolContext(toolCallId, ctx)
	})
}

// WrapRegisteredTools wraps all RegisteredTools into []*agent.AgentTool.
func WrapRegisteredTools(registeredTools []RegisteredTool, runner *ExtensionRunner) []*agent.AgentTool {
	out := make([]*agent.AgentTool, len(registeredTools))
	for i, rt := range registeredTools {
		out[i] = WrapRegisteredTool(rt, runner)
	}
	return out
}
