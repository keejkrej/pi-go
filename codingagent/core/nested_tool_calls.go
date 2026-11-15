// Ported from packages/coding-agent/src/core/nested-tool-calls.ts (pi v1.0.0).

package core

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// NestedCallLimits defines bounds for persisted nested tool call records on a tool result message.
var NestedCallLimits = struct {
	MaxCalls                int
	MaxArgumentBytesPerCall int
	MaxArgumentBytesTotal   int
	MaxErrorChars           int
}{
	MaxCalls:                256,
	MaxArgumentBytesPerCall: 8 * 1024,
	MaxArgumentBytesTotal:   32 * 1024,
	MaxErrorChars:           500,
}

// NestedCallSummary captures the recorded nested calls and summed usage of one model-issued tool call.
type NestedCallSummary struct {
	Calls *ai.NestedToolCalls `json:"calls,omitzero"`
	Usage *ai.Usage           `json:"usage,omitzero"`
}

// NestedCallRecorder collects nested calls and usage below one model-issued tool call.
type NestedCallRecorder struct {
	mu            sync.Mutex
	calls         []*ai.NestedToolCallRecord
	startedAt     map[*ai.NestedToolCallRecord]time.Time
	incomplete    bool
	argumentBytes int
	usage         *ai.Usage
}

// NewNestedCallRecorder constructs an empty NestedCallRecorder.
func NewNestedCallRecorder() *NestedCallRecorder {
	return &NestedCallRecorder{
		startedAt: make(map[*ai.NestedToolCallRecord]time.Time),
	}
}

// Start records a nested tool call when it begins. Returns nil if the call cap is exceeded.
func (r *NestedCallRecorder) Start(toolCall *agent.AgentToolCall) *ai.NestedToolCallRecord {
	if r == nil || toolCall == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.calls) >= NestedCallLimits.MaxCalls {
		r.incomplete = true
		return nil
	}

	record := &ai.NestedToolCallRecord{
		Id:     toolCall.Id,
		Name:   toolCall.Name,
		Status: ai.NestedToolCallStatusUnfinished,
	}

	args := toolCall.Arguments
	if args == nil {
		args = jsonx.NewObject()
	}
	rawJson, err := jsonx.Marshal(args)
	if err != nil {
		rawJson = []byte("{}")
	}
	bytesLen := len(rawJson)
	if bytesLen > NestedCallLimits.MaxArgumentBytesPerCall ||
		r.argumentBytes+bytesLen > NestedCallLimits.MaxArgumentBytesTotal {
		record.ArgumentsBytes = &bytesLen
		r.incomplete = true
	} else {
		var cloned *jsonx.Object
		if uErr := jsonx.Unmarshal(rawJson, &cloned); uErr == nil {
			record.Arguments = cloned
		} else {
			record.Arguments = jsonx.NewObject()
		}
		r.argumentBytes += bytesLen
	}

	if r.startedAt == nil {
		r.startedAt = make(map[*ai.NestedToolCallRecord]time.Time)
	}
	r.calls = append(r.calls, record)
	r.startedAt[record] = time.Now()
	return record
}

// Finish finalizes status, duration, and optional error text for a recorded call.
func (r *NestedCallRecorder) Finish(record *ai.NestedToolCallRecord, isError bool, errorText string) {
	if r == nil || record == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if isError {
		record.Status = ai.NestedToolCallStatusError
	} else {
		record.Status = ai.NestedToolCallStatusOk
	}
	dur := int64(0)
	if started, ok := r.startedAt[record]; ok {
		dur = time.Since(started).Milliseconds()
		delete(r.startedAt, record)
	}
	record.DurationMs = &dur
	if isError && errorText != "" {
		clipped := js.Slice(errorText, 0, NestedCallLimits.MaxErrorChars)
		record.Error = &clipped
	}
}

// AddUsage accumulates usage from one nested call into the parent summary.
func (r *NestedCallRecorder) AddUsage(usage *ai.Usage) {
	if r == nil || usage == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.usage == nil {
		u := *usage
		r.usage = &u
		return
	}
	combined := CombineUsage(*r.usage, *usage)
	r.usage = &combined
}

// TotalUsage returns a copy of accumulated nested usage, or nil if no usage was recorded.
func (r *NestedCallRecorder) TotalUsage() *ai.Usage {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.usage == nil {
		return nil
	}
	u := *r.usage
	return &u
}

// Snapshot returns a copy of the nested call record so far, or nil when no nested call was made.
func (r *NestedCallRecorder) Snapshot() *ai.NestedToolCalls {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.calls) == 0 && !r.incomplete {
		return nil
	}
	calls := make([]ai.NestedToolCallRecord, len(r.calls))
	allFinished := true
	for i, c := range r.calls {
		calls[i] = *c
		if c.Status == ai.NestedToolCallStatusUnfinished {
			allFinished = false
		}
	}
	return &ai.NestedToolCalls{
		Calls:    calls,
		Complete: !r.incomplete && allFinished,
	}
}

// NestedToolCallOptions configures an individual NestedToolCallRunner.Execute invocation.
type NestedToolCallOptions struct {
	Signal   context.Context
	OnUpdate agent.AgentToolUpdateCallback
}

// NestedToolExecutionEvent is emitted for nested tool_execution_{start,update,end} lifecycle updates.
type NestedToolExecutionEvent interface {
	ExtensionEvent
	isNestedToolExecutionEvent()
}

// NestedToolCallHost bridges NestedToolCallRunner to the enclosing session's tool pipeline and events.
type NestedToolCallHost interface {
	GetTools() []*agent.AgentTool
	IsSequential() bool
	RunToolCall(
		ctx context.Context,
		toolCall *agent.AgentToolCall,
		parentToolCallId string,
		onUpdate func(ctx context.Context, partialResult agent.AgentToolResult) error,
	) (agent.AgentToolCallOutcome, error)
	Emit(ctx context.Context, event NestedToolExecutionEvent) error
}

type ntcCallScope struct {
	recorder   *NestedCallRecorder
	nextId     int
	holdsQueue bool
}

func ntcTextOf(result agent.AgentToolResult) string {
	if len(result.Content) == 0 {
		return ""
	}
	var parts []string
	for _, block := range result.Content {
		if tc, ok := block.(*ai.TextContent); ok && tc != nil {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// NestedToolCallRunner orchestrates nested ctx.executeTool calls below active model-issued tool calls.
type NestedToolCallRunner struct {
	host      NestedToolCallHost
	mu        sync.Mutex
	scopes    map[string]*ntcCallScope
	queueMu   sync.Mutex
	queueTail chan struct{}
}

// NewNestedToolCallRunner creates a NestedToolCallRunner backed by host.
func NewNestedToolCallRunner(host NestedToolCallHost) *NestedToolCallRunner {
	return &NestedToolCallRunner{
		host:   host,
		scopes: make(map[string]*ntcCallScope),
	}
}

func (r *NestedToolCallRunner) acquireExclusive() func() {
	r.queueMu.Lock()
	prev := r.queueTail
	next := make(chan struct{})
	r.queueTail = next
	r.queueMu.Unlock()

	if prev != nil {
		<-prev
	}
	return func() {
		close(next)
	}
}

func ntcNormalizeArgs(args any) any {
	if args == nil {
		return jsonx.NewObject()
	}
	return args
}

// Execute runs tool name on behalf of callerId, assigning id <callerId>/<n>.
// Tool execution failures return an outcome with IsError true rather than failing the runner.
func (r *NestedToolCallRunner) Execute(
	ctx context.Context,
	callerId string,
	name string,
	args any,
	options ...*NestedToolCallOptions,
) (agent.AgentToolCallOutcome, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var opt NestedToolCallOptions
	if len(options) > 0 && options[0] != nil {
		opt = *options[0]
	}
	sigCtx := ctx
	if opt.Signal != nil {
		sigCtx = opt.Signal
	}

	r.mu.Lock()
	scope, ok := r.scopes[callerId]
	if !ok {
		scope = &ntcCallScope{
			recorder: NewNestedCallRecorder(),
			nextId:   1,
		}
		r.scopes[callerId] = scope
	}
	seq := scope.nextId
	scope.nextId++
	holdsQueue := scope.holdsQueue
	r.mu.Unlock()

	toolCall := &agent.AgentToolCall{
		Type:      "toolCall",
		Id:        fmt.Sprintf("%s/%d", callerId, seq),
		Name:      name,
		Arguments: ntcNormalizeArgs(args),
	}
	record := scope.recorder.Start(toolCall)

	parentId := callerId
	if err := r.host.Emit(sigCtx, &ToolExecutionStartEvent{
		Type:             "tool_execution_start",
		ToolCallId:       toolCall.Id,
		ToolName:         name,
		Args:             toolCall.Arguments,
		ParentToolCallId: &parentId,
	}); err != nil {
		return agent.AgentToolCallOutcome{}, err
	}

	exclusive := !holdsQueue && r.isExclusive(name)
	var release func()
	if exclusive {
		release = r.acquireExclusive()
	}

	r.mu.Lock()
	r.scopes[toolCall.Id] = &ntcCallScope{
		recorder:   scope.recorder,
		nextId:     1,
		holdsQueue: holdsQueue || exclusive,
	}
	r.mu.Unlock()

	var outcome agent.AgentToolCallOutcome
	var runErr error
	func() {
		defer func() {
			r.mu.Lock()
			delete(r.scopes, toolCall.Id)
			r.mu.Unlock()
			if release != nil {
				release()
			}
		}()

		outcome, runErr = r.host.RunToolCall(
			sigCtx,
			toolCall,
			callerId,
			func(uCtx context.Context, partialResult agent.AgentToolResult) error {
				if opt.OnUpdate != nil {
					opt.OnUpdate(partialResult)
				}
				return r.host.Emit(uCtx, &ToolExecutionUpdateEvent{
					Type:             "tool_execution_update",
					ToolCallId:       toolCall.Id,
					ToolName:         name,
					Args:             toolCall.Arguments,
					PartialResult:    partialResult,
					ParentToolCallId: &parentId,
				})
			},
		)
	}()
	if runErr != nil {
		return agent.AgentToolCallOutcome{}, runErr
	}

	scope.recorder.Finish(record, outcome.IsError, ntcTextOf(outcome.Result))
	if outcome.Result.Usage != nil {
		scope.recorder.AddUsage(outcome.Result.Usage)
	}
	if err := r.host.Emit(sigCtx, &ToolExecutionEndEvent{
		Type:             "tool_execution_end",
		ToolCallId:       toolCall.Id,
		ToolName:         name,
		Result:           outcome.Result,
		IsError:          outcome.IsError,
		DurationMs:       outcome.DurationMs,
		ParentToolCallId: &parentId,
	}); err != nil {
		return outcome, err
	}
	return outcome, nil
}

func (r *NestedToolCallRunner) isExclusive(name string) bool {
	if r.host.IsSequential() {
		return true
	}
	for _, tool := range r.host.GetTools() {
		if tool != nil && tool.Name == name {
			return tool.ExecutionMode == agent.ToolExecutionModeSequential
		}
	}
	return false
}

// TakeRecord removes and returns the nested call summary recorded for toolCallId.
func (r *NestedToolCallRunner) TakeRecord(toolCallId string) *NestedCallSummary {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	scope, ok := r.scopes[toolCallId]
	if ok {
		delete(r.scopes, toolCallId)
	}
	r.mu.Unlock()
	if !ok || scope == nil {
		return nil
	}
	return &NestedCallSummary{
		Calls: scope.recorder.Snapshot(),
		Usage: scope.recorder.TotalUsage(),
	}
}

// Clear resets all recorded scopes.
func (r *NestedToolCallRunner) Clear() {
	if r == nil {
		return
	}
	r.mu.Lock()
	clear(r.scopes)
	r.mu.Unlock()
}
