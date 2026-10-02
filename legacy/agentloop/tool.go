package agentloop

import "context"

// ExecutionMode controls whether a tool runs sequentially or in parallel.
type ExecutionMode string

// ExecutionMode values.
const (
	ExecutionDefault    ExecutionMode = ""           // use loop default
	ExecutionSequential ExecutionMode = "sequential" //nolint:revive
	ExecutionParallel   ExecutionMode = "parallel"
)

// ToolResult is the outcome of a tool execution.
type ToolResult struct {
	Content   []Content // text + image
	Details   any
	Terminate bool // hint: stop after this batch (only if EVERY result in batch sets it)
}

// UpdateFunc streams a partial tool result.
type UpdateFunc func(partial ToolResult)

// AgentTool is a callable tool exposed to the model.
type AgentTool interface {
	Name() string
	Label() string
	Description() string
	Parameters() map[string]any // JSON Schema (object). nil/empty => no params.
	ExecutionMode() ExecutionMode
	// PrepareArguments optionally normalizes raw args before validation.
	// Return (out, true) to replace; (_, false) to leave unchanged.
	PrepareArguments(raw map[string]any) (map[string]any, bool)
	// Execute runs the tool. Return an error to signal failure (the loop
	// converts it to an error tool-result). onUpdate may be nil; if non-nil,
	// call it to stream partial results. Honor ctx cancellation.
	Execute(ctx context.Context, toolCallID string, params map[string]any, onUpdate UpdateFunc) (ToolResult, error)
}

// FuncTool is a convenience AgentTool built from fields and closures.
type FuncTool struct {
	NameVal, LabelVal, DescriptionVal string
	ParametersVal                     map[string]any
	Mode                              ExecutionMode
	Prepare                           func(map[string]any) (map[string]any, bool)
	ExecuteFn                         func(ctx context.Context, toolCallID string, params map[string]any, onUpdate UpdateFunc) (ToolResult, error)
}

// Name returns the tool name.
func (f *FuncTool) Name() string { return f.NameVal }

// Label returns the tool label.
func (f *FuncTool) Label() string { return f.LabelVal }

// Description returns the tool description.
func (f *FuncTool) Description() string { return f.DescriptionVal }

// Parameters returns the JSON-Schema parameters.
func (f *FuncTool) Parameters() map[string]any { return f.ParametersVal }

// ExecutionMode returns the tool's execution mode.
func (f *FuncTool) ExecutionMode() ExecutionMode { return f.Mode }

// PrepareArguments normalizes raw args; returns (raw, false) when Prepare is nil.
func (f *FuncTool) PrepareArguments(raw map[string]any) (map[string]any, bool) {
	if f.Prepare == nil {
		return raw, false
	}
	return f.Prepare(raw)
}

// Execute runs the tool's ExecuteFn.
func (f *FuncTool) Execute(ctx context.Context, toolCallID string, params map[string]any, onUpdate UpdateFunc) (ToolResult, error) {
	return f.ExecuteFn(ctx, toolCallID, params, onUpdate)
}

// TextResult is a helper: a ToolResult with a single text block.
func TextResult(text string) ToolResult {
	return ToolResult{Content: []Content{&TextContent{Text: text}}}
}

// ErrorResultContent builds the content for an error tool result.
func ErrorResultContent(msg string) []Content {
	return []Content{&TextContent{Text: msg}}
}
