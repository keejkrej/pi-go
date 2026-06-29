package agentloop

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// emitSink is a serialized event sink. All emit() calls go through the mutex so
// events never interleave when emitted from multiple goroutines (the parallel
// tool path).
type emitSink struct {
	mu     sync.Mutex
	stream *EventStream[AgentEvent, []AgentMessage]
}

func (e *emitSink) emit(ev AgentEvent) {
	e.mu.Lock()
	e.stream.Push(ev)
	e.mu.Unlock()
}

func newAgentStream() *EventStream[AgentEvent, []AgentMessage] {
	return NewEventStream[AgentEvent, []AgentMessage](
		func(ev AgentEvent) bool {
			_, ok := ev.(*AgentEndEvent)
			return ok
		},
		func(ev AgentEvent) []AgentMessage {
			if e, ok := ev.(*AgentEndEvent); ok {
				return e.Messages
			}
			return nil
		},
	)
}

// AgentLoop starts a run with new prompt messages appended to the context. The
// returned stream's terminal event is AgentEndEvent, whose Messages slice is the
// run result (Result() returns that slice).
func AgentLoop(ctx context.Context, prompts []AgentMessage, agentCtx *AgentContext,
	config *AgentLoopConfig, streamFn StreamFn) *EventStream[AgentEvent, []AgentMessage] {
	stream := newAgentStream()
	sink := &emitSink{stream: stream}

	go func() {
		messages := runAgentLoop(ctx, prompts, agentCtx, config, sink, streamFn)
		stream.End(messages)
	}()

	return stream
}

// AgentLoopContinue resumes from an existing context without adding a message.
// If the context is empty or its last message role is "assistant", the returned
// stream immediately ends with an empty result (the error is encoded by ending
// without an agent_end event).
func AgentLoopContinue(ctx context.Context, agentCtx *AgentContext,
	config *AgentLoopConfig, streamFn StreamFn) *EventStream[AgentEvent, []AgentMessage] {
	stream := newAgentStream()
	sink := &emitSink{stream: stream}

	if len(agentCtx.Messages) == 0 ||
		agentCtx.Messages[len(agentCtx.Messages)-1].Role() == "assistant" {
		stream.End(nil)
		return stream
	}

	go func() {
		messages := runAgentLoopContinue(ctx, agentCtx, config, sink, streamFn)
		stream.End(messages)
	}()

	return stream
}

func runAgentLoop(ctx context.Context, prompts []AgentMessage, agentCtx *AgentContext,
	config *AgentLoopConfig, sink *emitSink, streamFn StreamFn) []AgentMessage {
	newMessages := append([]AgentMessage(nil), prompts...)

	currentContext := &AgentContext{
		SystemPrompt: agentCtx.SystemPrompt,
		Messages:     append(append([]AgentMessage(nil), agentCtx.Messages...), prompts...),
		Tools:        agentCtx.Tools,
	}

	sink.emit(&AgentStartEvent{})
	sink.emit(&TurnStartEvent{})
	for _, p := range prompts {
		sink.emit(&MessageStartEvent{Message: p})
		sink.emit(&MessageEndEvent{Message: p})
	}

	runLoop(ctx, currentContext, &newMessages, config, sink, streamFn)
	return newMessages
}

func runAgentLoopContinue(ctx context.Context, agentCtx *AgentContext,
	config *AgentLoopConfig, sink *emitSink, streamFn StreamFn) []AgentMessage {
	var newMessages []AgentMessage

	// Shallow copy of context (same Messages slice ref as TS {...context}).
	currentContext := &AgentContext{
		SystemPrompt: agentCtx.SystemPrompt,
		Messages:     agentCtx.Messages,
		Tools:        agentCtx.Tools,
	}

	sink.emit(&AgentStartEvent{})
	sink.emit(&TurnStartEvent{})

	runLoop(ctx, currentContext, &newMessages, config, sink, streamFn)
	return newMessages
}

func runLoop(ctx context.Context, currentContext *AgentContext, newMessages *[]AgentMessage,
	initialConfig *AgentLoopConfig, sink *emitSink, streamFn StreamFn) {
	config := initialConfig
	firstTurn := true

	var pendingMessages []AgentMessage
	if config.GetSteeringMessages != nil {
		if msgs, err := config.GetSteeringMessages(); err == nil {
			pendingMessages = msgs
		}
	}

	for { // outer follow-up loop
		hasMoreToolCalls := true

		for hasMoreToolCalls || len(pendingMessages) > 0 { // inner loop
			if !firstTurn {
				sink.emit(&TurnStartEvent{})
			} else {
				firstTurn = false
			}

			if len(pendingMessages) > 0 {
				for _, message := range pendingMessages {
					sink.emit(&MessageStartEvent{Message: message})
					sink.emit(&MessageEndEvent{Message: message})
					currentContext.Messages = append(currentContext.Messages, message)
					*newMessages = append(*newMessages, message)
				}
				pendingMessages = nil
			}

			message := streamAssistantResponse(ctx, currentContext, config, sink, streamFn)
			*newMessages = append(*newMessages, message)

			if message.StopReason == StopReasonError || message.StopReason == StopReasonAborted {
				sink.emit(&TurnEndEvent{Message: message, ToolResults: []*ToolResultMessage{}})
				sink.emit(&AgentEndEvent{Messages: *newMessages})
				return
			}

			toolCalls := message.ToolCalls()
			toolResults := []*ToolResultMessage{}
			hasMoreToolCalls = false
			if len(toolCalls) > 0 {
				batch := executeToolCalls(ctx, currentContext, message, config, sink)
				toolResults = append(toolResults, batch.Messages...)
				hasMoreToolCalls = !batch.Terminate
				for _, result := range toolResults {
					currentContext.Messages = append(currentContext.Messages, result)
					*newMessages = append(*newMessages, result)
				}
			}

			sink.emit(&TurnEndEvent{Message: message, ToolResults: toolResults})

			if config.PrepareNextTurn != nil {
				update, err := config.PrepareNextTurn(PrepareNextTurnContext{
					Message:     message,
					ToolResults: toolResults,
					Context:     currentContext,
					NewMessages: *newMessages,
				})
				if err == nil && update != nil {
					if update.Context != nil {
						currentContext = update.Context
					}
					next := config.clone()
					if update.Model != nil {
						next.Model = update.Model
					}
					if update.HasThinking {
						if update.ThinkingLevel == ThinkingOff {
							next.Reasoning = ""
						} else {
							next.Reasoning = update.ThinkingLevel
						}
					}
					config = next
				}
			}

			if config.ShouldStopAfterTurn != nil {
				stop, err := config.ShouldStopAfterTurn(ShouldStopAfterTurnContext{
					Message:     message,
					ToolResults: toolResults,
					Context:     currentContext,
					NewMessages: *newMessages,
				})
				if err == nil && stop {
					sink.emit(&AgentEndEvent{Messages: *newMessages})
					return
				}
			}

			pendingMessages = nil
			if config.GetSteeringMessages != nil {
				if msgs, err := config.GetSteeringMessages(); err == nil {
					pendingMessages = msgs
				}
			}
		}

		var followUp []AgentMessage
		if config.GetFollowUpMessages != nil {
			if msgs, err := config.GetFollowUpMessages(); err == nil {
				followUp = msgs
			}
		}
		if len(followUp) > 0 {
			pendingMessages = followUp
			continue
		}
		break
	}

	sink.emit(&AgentEndEvent{Messages: *newMessages})
}

func copyAssistant(m *AssistantMessage) *AssistantMessage {
	cp := *m
	return &cp
}

func streamAssistantResponse(ctx context.Context, agentCtx *AgentContext,
	config *AgentLoopConfig, sink *emitSink, streamFn StreamFn) *AssistantMessage {
	messages := agentCtx.Messages
	if config.TransformContext != nil {
		if transformed, err := safeTransform(ctx, config.TransformContext, messages); err == nil {
			messages = transformed
		}
	}

	llmMessages, err := safeConvertToLlm(config.ConvertToLlm, messages)
	if err != nil {
		// ConvertToLlm must not fail; defensively synthesize an error message.
		final := &AssistantMessage{
			Api:          modelApi(config.Model),
			Provider:     modelProvider(config.Model),
			Model:        modelID(config.Model),
			StopReason:   StopReasonError,
			ErrorMessage: err.Error(),
			Timestamp:    time.Now().UnixMilli(),
		}
		agentCtx.Messages = append(agentCtx.Messages, final)
		sink.emit(&MessageStartEvent{Message: copyAssistant(final)})
		sink.emit(&MessageEndEvent{Message: final})
		return final
	}

	wireCtx := &Context{
		SystemPrompt: agentCtx.SystemPrompt,
		Messages:     llmMessages,
		Tools:        agentCtx.Tools,
	}

	key := config.APIKey
	if config.GetApiKey != nil {
		if k, err := config.GetApiKey(modelProvider(config.Model)); err == nil && k != "" {
			key = k
		}
	}

	opts := &StreamOptions{
		Temperature: config.Temperature,
		MaxTokens:   config.MaxTokens,
		APIKey:      key,
		Reasoning:   config.Reasoning,
	}

	response := streamFn(ctx, config.Model, wireCtx, opts)

	var partial *AssistantMessage
	addedPartial := false

	for ev := range response.Events() {
		switch e := ev.(type) {
		case *StartEvent:
			partial = e.Partial
			agentCtx.Messages = append(agentCtx.Messages, partial)
			addedPartial = true
			sink.emit(&MessageStartEvent{Message: copyAssistant(partial)})

		case *DoneEvent, *ErrorEvent:
			final := response.Result()
			if addedPartial {
				agentCtx.Messages[len(agentCtx.Messages)-1] = final
			} else {
				agentCtx.Messages = append(agentCtx.Messages, final)
				sink.emit(&MessageStartEvent{Message: copyAssistant(final)})
			}
			sink.emit(&MessageEndEvent{Message: final})
			return final

		default:
			// text/thinking/toolcall start/delta/end events.
			if partial != nil {
				partial = eventPartial(ev)
				if partial != nil {
					agentCtx.Messages[len(agentCtx.Messages)-1] = partial
					sink.emit(&MessageUpdateEvent{
						Message:               copyAssistant(partial),
						AssistantMessageEvent: ev,
					})
				}
			}
		}
	}

	// Stream ended without an in-band done/error event (mirror TS tail).
	final := response.Result()
	if final == nil {
		final = &AssistantMessage{
			Api:        modelApi(config.Model),
			Provider:   modelProvider(config.Model),
			Model:      modelID(config.Model),
			StopReason: StopReasonStop,
			Timestamp:  time.Now().UnixMilli(),
		}
	}
	if addedPartial {
		agentCtx.Messages[len(agentCtx.Messages)-1] = final
	} else {
		agentCtx.Messages = append(agentCtx.Messages, final)
		sink.emit(&MessageStartEvent{Message: copyAssistant(final)})
	}
	sink.emit(&MessageEndEvent{Message: final})
	return final
}

// eventPartial extracts the Partial field from an in-stream assistant event.
func eventPartial(ev AssistantMessageEvent) *AssistantMessage {
	switch e := ev.(type) {
	case *TextStartEvent:
		return e.Partial
	case *TextDeltaEvent:
		return e.Partial
	case *TextEndEvent:
		return e.Partial
	case *ThinkingStartEvent:
		return e.Partial
	case *ThinkingDeltaEvent:
		return e.Partial
	case *ThinkingEndEvent:
		return e.Partial
	case *ToolCallStartEvent:
		return e.Partial
	case *ToolCallDeltaEvent:
		return e.Partial
	case *ToolCallEndEvent:
		return e.Partial
	default:
		return nil
	}
}

func modelApi(m *Model) string {
	if m == nil {
		return ""
	}
	return m.Api
}
func modelProvider(m *Model) string {
	if m == nil {
		return ""
	}
	return m.Provider
}
func modelID(m *Model) string {
	if m == nil {
		return ""
	}
	return m.ID
}

func safeTransform(ctx context.Context, fn func(context.Context, []AgentMessage) ([]AgentMessage, error),
	messages []AgentMessage) (out []AgentMessage, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("transformContext panic: %v", r)
		}
	}()
	return fn(ctx, messages)
}

func safeConvertToLlm(fn func([]AgentMessage) ([]Message, error),
	messages []AgentMessage) (out []Message, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("convertToLlm panic: %v", r)
		}
	}()
	if fn == nil {
		return nil, fmt.Errorf("convertToLlm is required")
	}
	return fn(messages)
}

// executedToolCallBatch is the result of executing a tool-call batch.
type executedToolCallBatch struct {
	Messages  []*ToolResultMessage
	Terminate bool
}

type finalizedToolCall struct {
	toolCall *ToolCall
	result   ToolResult
	isError  bool
}

type preparedToolCall struct {
	immediate bool
	// immediate fields:
	result  ToolResult
	isError bool
	// prepared fields:
	toolCall *ToolCall
	tool     AgentTool
	args     map[string]any
}

func executeToolCalls(ctx context.Context, currentContext *AgentContext,
	assistantMessage *AssistantMessage, config *AgentLoopConfig, sink *emitSink) executedToolCallBatch {
	toolCalls := assistantMessage.ToolCalls()

	hasSequential := false
	for _, tc := range toolCalls {
		if tool := findTool(currentContext.Tools, tc.Name); tool != nil && tool.ExecutionMode() == ExecutionSequential {
			hasSequential = true
			break
		}
	}

	if config.ToolExecution == ExecutionSequential || hasSequential {
		return executeToolCallsSequential(ctx, currentContext, assistantMessage, toolCalls, config, sink)
	}
	return executeToolCallsParallel(ctx, currentContext, assistantMessage, toolCalls, config, sink)
}

func executeToolCallsSequential(ctx context.Context, currentContext *AgentContext,
	assistantMessage *AssistantMessage, toolCalls []*ToolCall, config *AgentLoopConfig,
	sink *emitSink) executedToolCallBatch {
	var finalizedCalls []finalizedToolCall
	var messages []*ToolResultMessage

	for _, toolCall := range toolCalls {
		sink.emit(&ToolExecutionStartEvent{
			ToolCallID: toolCall.ID,
			ToolName:   toolCall.Name,
			Args:       toolCall.Arguments,
		})

		prep := prepareToolCall(ctx, currentContext, assistantMessage, toolCall, config)
		var finalized finalizedToolCall
		if prep.immediate {
			finalized = finalizedToolCall{toolCall: toolCall, result: prep.result, isError: prep.isError}
		} else {
			executed := executePreparedToolCall(ctx, &prep, sink)
			finalized = finalizeExecutedToolCall(currentContext, assistantMessage, &prep, executed, config)
		}

		emitToolExecutionEnd(finalized, sink)
		trm := createToolResultMessage(finalized)
		emitToolResultMessage(trm, sink)
		finalizedCalls = append(finalizedCalls, finalized)
		messages = append(messages, trm)

		if ctx.Err() != nil {
			break
		}
	}

	return executedToolCallBatch{
		Messages:  messages,
		Terminate: shouldTerminateToolBatch(finalizedCalls),
	}
}

func executeToolCallsParallel(ctx context.Context, currentContext *AgentContext,
	assistantMessage *AssistantMessage, toolCalls []*ToolCall, config *AgentLoopConfig,
	sink *emitSink) executedToolCallBatch {
	// Each entry is resolved either immediately during preflight or by a
	// deferred thunk run concurrently. ordered preserves source order.
	type entry struct {
		resolved  bool
		finalized finalizedToolCall
		thunk     func() finalizedToolCall
	}
	entries := make([]entry, 0, len(toolCalls))

	for _, toolCall := range toolCalls {
		sink.emit(&ToolExecutionStartEvent{
			ToolCallID: toolCall.ID,
			ToolName:   toolCall.Name,
			Args:       toolCall.Arguments,
		})

		prep := prepareToolCall(ctx, currentContext, assistantMessage, toolCall, config)
		if prep.immediate {
			finalized := finalizedToolCall{toolCall: toolCall, result: prep.result, isError: prep.isError}
			emitToolExecutionEnd(finalized, sink)
			entries = append(entries, entry{resolved: true, finalized: finalized})
			if ctx.Err() != nil {
				break
			}
			continue
		}

		p := prep // capture
		entries = append(entries, entry{
			thunk: func() finalizedToolCall {
				executed := executePreparedToolCall(ctx, &p, sink)
				finalized := finalizeExecutedToolCall(currentContext, assistantMessage, &p, executed, config)
				emitToolExecutionEnd(finalized, sink)
				return finalized
			},
		})
		if ctx.Err() != nil {
			break
		}
	}

	// Run deferred thunks concurrently. tool_execution_end emits happen in
	// completion order (inside thunks); ordered is filled by source index.
	ordered := make([]finalizedToolCall, len(entries))
	var wg sync.WaitGroup
	for i := range entries {
		if entries[i].resolved {
			ordered[i] = entries[i].finalized
			continue
		}
		wg.Add(1)
		go func(idx int, thunk func() finalizedToolCall) {
			defer wg.Done()
			ordered[idx] = thunk()
		}(i, entries[i].thunk)
	}
	wg.Wait()

	// Result messages are emitted afterward in SOURCE order.
	var messages []*ToolResultMessage
	for _, finalized := range ordered {
		trm := createToolResultMessage(finalized)
		emitToolResultMessage(trm, sink)
		messages = append(messages, trm)
	}

	return executedToolCallBatch{
		Messages:  messages,
		Terminate: shouldTerminateToolBatch(ordered),
	}
}

func shouldTerminateToolBatch(finalizedCalls []finalizedToolCall) bool {
	if len(finalizedCalls) == 0 {
		return false
	}
	for _, f := range finalizedCalls {
		if !f.result.Terminate {
			return false
		}
	}
	return true
}

func findTool(tools []AgentTool, name string) AgentTool {
	for _, t := range tools {
		if t.Name() == name {
			return t
		}
	}
	return nil
}

func prepareToolCallArguments(tool AgentTool, toolCall *ToolCall) *ToolCall {
	out, changed := tool.PrepareArguments(toolCall.Arguments)
	if !changed {
		return toolCall
	}
	cp := *toolCall
	cp.Arguments = out
	return &cp
}

func prepareToolCall(ctx context.Context, currentContext *AgentContext,
	assistantMessage *AssistantMessage, toolCall *ToolCall, config *AgentLoopConfig) (prep preparedToolCall) {
	defer func() {
		if r := recover(); r != nil {
			prep = preparedToolCall{
				immediate: true,
				result:    createErrorToolResult(fmt.Sprintf("%v", r)),
				isError:   true,
			}
		}
	}()

	tool := findTool(currentContext.Tools, toolCall.Name)
	if tool == nil {
		return preparedToolCall{
			immediate: true,
			result:    createErrorToolResult(fmt.Sprintf("Tool %s not found", toolCall.Name)),
			isError:   true,
		}
	}

	preparedToolCallArgs := prepareToolCallArguments(tool, toolCall)
	validatedArgs, err := ValidateToolArguments(tool, preparedToolCallArgs)
	if err != nil {
		return preparedToolCall{
			immediate: true,
			result:    createErrorToolResult(err.Error()),
			isError:   true,
		}
	}

	if config.BeforeToolCall != nil {
		beforeResult, berr := config.BeforeToolCall(BeforeToolCallContext{
			AssistantMessage: assistantMessage,
			ToolCall:         toolCall,
			Args:             validatedArgs,
			Context:          currentContext,
		})
		if berr != nil {
			return preparedToolCall{
				immediate: true,
				result:    createErrorToolResult(berr.Error()),
				isError:   true,
			}
		}
		if ctx.Err() != nil {
			return preparedToolCall{
				immediate: true,
				result:    createErrorToolResult("Operation aborted"),
				isError:   true,
			}
		}
		if beforeResult != nil && beforeResult.Block {
			reason := beforeResult.Reason
			if reason == "" {
				reason = "Tool execution was blocked"
			}
			return preparedToolCall{
				immediate: true,
				result:    createErrorToolResult(reason),
				isError:   true,
			}
		}
	}

	if ctx.Err() != nil {
		return preparedToolCall{
			immediate: true,
			result:    createErrorToolResult("Operation aborted"),
			isError:   true,
		}
	}

	return preparedToolCall{
		toolCall: toolCall,
		tool:     tool,
		args:     validatedArgs,
	}
}

type executedToolCallOutcome struct {
	result  ToolResult
	isError bool
}

func executePreparedToolCall(ctx context.Context, prep *preparedToolCall, sink *emitSink) (outcome executedToolCallOutcome) {
	var mu sync.Mutex
	accepting := true

	onUpdate := func(partial ToolResult) {
		mu.Lock()
		ok := accepting
		mu.Unlock()
		if !ok {
			return
		}
		sink.emit(&ToolExecutionUpdateEvent{
			ToolCallID:    prep.toolCall.ID,
			ToolName:      prep.toolCall.Name,
			Args:          prep.toolCall.Arguments,
			PartialResult: partial,
		})
	}

	defer func() {
		mu.Lock()
		accepting = false
		mu.Unlock()
		if r := recover(); r != nil {
			outcome = executedToolCallOutcome{
				result:  createErrorToolResult(fmt.Sprintf("%v", r)),
				isError: true,
			}
		}
	}()

	result, err := prep.tool.Execute(ctx, prep.toolCall.ID, prep.args, onUpdate)
	mu.Lock()
	accepting = false
	mu.Unlock()
	if err != nil {
		return executedToolCallOutcome{result: createErrorToolResult(err.Error()), isError: true}
	}
	return executedToolCallOutcome{result: result, isError: false}
}

func finalizeExecutedToolCall(currentContext *AgentContext, assistantMessage *AssistantMessage,
	prep *preparedToolCall, executed executedToolCallOutcome, config *AgentLoopConfig) (finalized finalizedToolCall) {
	result := executed.result
	isError := executed.isError

	if config.AfterToolCall != nil {
		func() {
			defer func() {
				if r := recover(); r != nil {
					result = createErrorToolResult(fmt.Sprintf("%v", r))
					isError = true
				}
			}()
			afterResult, err := config.AfterToolCall(AfterToolCallContext{
				AssistantMessage: assistantMessage,
				ToolCall:         prep.toolCall,
				Args:             prep.args,
				Result:           result,
				IsError:          isError,
				Context:          currentContext,
			})
			if err != nil {
				result = createErrorToolResult(err.Error())
				isError = true
				return
			}
			if afterResult != nil {
				newResult := ToolResult{
					Content:   result.Content,
					Details:   result.Details,
					Terminate: result.Terminate,
				}
				if afterResult.HasContent {
					newResult.Content = afterResult.Content
				}
				if afterResult.HasDetails {
					newResult.Details = afterResult.Details
				}
				if afterResult.Terminate != nil {
					newResult.Terminate = *afterResult.Terminate
				}
				result = newResult
				if afterResult.IsError != nil {
					isError = *afterResult.IsError
				}
			}
		}()
	}

	return finalizedToolCall{toolCall: prep.toolCall, result: result, isError: isError}
}

func createErrorToolResult(message string) ToolResult {
	return ToolResult{
		Content: []Content{&TextContent{Text: message}},
		Details: map[string]any{},
	}
}

func emitToolExecutionEnd(finalized finalizedToolCall, sink *emitSink) {
	sink.emit(&ToolExecutionEndEvent{
		ToolCallID: finalized.toolCall.ID,
		ToolName:   finalized.toolCall.Name,
		Result:     finalized.result,
		IsError:    finalized.isError,
	})
}

func createToolResultMessage(finalized finalizedToolCall) *ToolResultMessage {
	return &ToolResultMessage{
		ToolCallID: finalized.toolCall.ID,
		ToolName:   finalized.toolCall.Name,
		Content:    finalized.result.Content,
		Details:    finalized.result.Details,
		IsError:    finalized.isError,
		Timestamp:  time.Now().UnixMilli(),
	}
}

func emitToolResultMessage(trm *ToolResultMessage, sink *emitSink) {
	sink.emit(&MessageStartEvent{Message: trm})
	sink.emit(&MessageEndEvent{Message: trm})
}
