// Ported from packages/coding-agent/src/core/tools/tool-definition-wrapper.ts (pi v1.0.0).

package core

import (
	"context"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

type ttdwCtxKey struct{}

// ToolContextFactory creates an ExtensionToolContext for one tool execution.
type ToolContextFactory func(ctx context.Context, toolCallId string) ExtensionToolContext

// WithExtensionToolContext attaches ectx to ctx so AgentTool.Execute can forward it to ToolDefinition.Execute.
func WithExtensionToolContext(ctx context.Context, ectx ExtensionToolContext) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, ttdwCtxKey{}, ectx)
}

// ExtensionToolContextFromContext retrieves the ExtensionToolContext previously attached with WithExtensionToolContext.
func ExtensionToolContextFromContext(ctx context.Context) ExtensionToolContext {
	if ctx == nil {
		return nil
	}
	ectx, _ := ctx.Value(ttdwCtxKey{}).(ExtensionToolContext)
	return ectx
}

func ttdwParamsToObject(params any) *jsonx.Object {
	if params == nil {
		return jsonx.NewObject()
	}
	if obj, ok := params.(*jsonx.Object); ok {
		if obj == nil {
			return jsonx.NewObject()
		}
		return obj
	}
	encoded, err := jsonx.Encode(params)
	if err == nil {
		if obj, ok := encoded.(*jsonx.Object); ok && obj != nil {
			return obj
		}
	}
	return jsonx.NewObject()
}

// WrapToolDefinition adapts a ToolDefinition into an *agent.AgentTool for the agent runtime.
func WrapToolDefinition(definition ToolDefinition, ctxFactory ...ToolContextFactory) *agent.AgentTool {
	var factory ToolContextFactory
	if len(ctxFactory) > 0 {
		factory = ctxFactory[0]
	}

	return &agent.AgentTool{
		Name:                definition.Name,
		Label:               definition.Label,
		Description:         definition.Description,
		Parameters:          definition.Parameters,
		OutputSchema:        definition.OutputSchema,
		ConstrainedSampling: definition.ConstrainedSampling,
		PrepareArguments:    definition.PrepareArguments,
		ExecutionMode:       definition.ExecutionMode,
		Execute: func(ctx context.Context, toolCallId string, params any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
			if definition.Execute == nil {
				return agent.AgentToolResult{}, nil
			}
			ectx := ExtensionToolContextFromContext(ctx)
			if ectx == nil && factory != nil {
				ectx = factory(ctx, toolCallId)
			}
			return definition.Execute(ctx, toolCallId, ttdwParamsToObject(params), onUpdate, ectx)
		},
	}
}

// WrapToolDefinitions adapts a slice of ToolDefinitions into []*agent.AgentTool.
func WrapToolDefinitions(definitions []ToolDefinition, ctxFactory ...ToolContextFactory) []*agent.AgentTool {
	out := make([]*agent.AgentTool, len(definitions))
	for i, def := range definitions {
		out[i] = WrapToolDefinition(def, ctxFactory...)
	}
	return out
}

// CreateToolDefinitionFromAgentTool synthesizes a minimal ToolDefinition from a plain *agent.AgentTool.
func CreateToolDefinitionFromAgentTool(tool *agent.AgentTool) ToolDefinition {
	if tool == nil {
		return ToolDefinition{}
	}
	return ToolDefinition{
		Name:                tool.Name,
		Label:               tool.Label,
		Description:         tool.Description,
		Parameters:          tool.Parameters,
		OutputSchema:        tool.OutputSchema,
		ConstrainedSampling: tool.ConstrainedSampling,
		PrepareArguments:    tool.PrepareArguments,
		ExecutionMode:       tool.ExecutionMode,
		Execute: func(ctx context.Context, toolCallId string, params *jsonx.Object, onUpdate agent.AgentToolUpdateCallback, _ ExtensionToolContext) (agent.AgentToolResult, error) {
			if tool.Execute == nil {
				return agent.AgentToolResult{}, nil
			}
			return tool.Execute(ctx, toolCallId, params, onUpdate)
		},
	}
}
