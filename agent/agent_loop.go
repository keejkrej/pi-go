// Ported from packages/agent/src/agent-loop.ts (pi v1.5.2).

package agent

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// DefaultAgentIdentity identifies requests originated by pi's agent loop.
var DefaultAgentIdentity = &ai.ClientIdentity{
	Attribution: jsonx.Some(ai.PiAttribution),
}

// AgentEventSink receives AgentEvents synchronously as the loop progresses.
type AgentEventSink func(event AgentEvent)

// AgentEventStream is the event stream returned by AgentLoop and AgentLoopContinue.
type AgentEventStream = ai.EventStream[AgentEvent, []AgentMessage]

// ToolCallHooks configures optional before/after tool hooks passed to RunToolCall.
type ToolCallHooks struct {
	BeforeToolCall func(ctx context.Context, tcContext *BeforeToolCallContext) (*BeforeToolCallResult, error)
	AfterToolCall  func(ctx context.Context, tcContext *AfterToolCallContext) (*AfterToolCallResult, error)
}

// RunToolCallOptions configures a single RunToolCall invocation.
type RunToolCallOptions struct {
	ToolCallHooks
	Tools            []*AgentTool
	AssistantMessage *ai.AssistantMessage
	Context          *AgentContext
	OnUpdate         AgentToolUpdateCallback
}

// NewAgentEventStream creates an AgentEventStream completing on agent_end.
func NewAgentEventStream() *AgentEventStream {
	return ai.NewEventStream(
		func(ev AgentEvent) bool {
			switch ev.(type) {
			case *AgentEventAgentEnd, AgentEventAgentEnd:
				return true
			default:
				return false
			}
		},
		func(ev AgentEvent) []AgentMessage {
			switch e := ev.(type) {
			case *AgentEventAgentEnd:
				return e.Messages
			case AgentEventAgentEnd:
				return e.Messages
			default:
				return nil
			}
		},
	)
}

// AgentLoop starts an agent loop with new prompt messages and streams AgentEvents asynchronously.
func AgentLoop(
	ctx context.Context,
	prompts []AgentMessage,
	agentContext *AgentContext,
	config *AgentLoopConfig,
	streamFn StreamFn,
) *AgentEventStream {
	stream := NewAgentEventStream()
	go func() {
		msgs, err := RunAgentLoop(ctx, prompts, agentContext, config, func(ev AgentEvent) {
			stream.Push(ev)
		}, streamFn)
		if err != nil {
			stream.End(nil)
			return
		}
		stream.End(msgs)
	}()
	return stream
}

// AgentLoopContinue continues an agent loop from an existing context without appending prompts.
// It fails synchronously if the context is empty or ends with an assistant message.
func AgentLoopContinue(
	ctx context.Context,
	agentContext *AgentContext,
	config *AgentLoopConfig,
	streamFn StreamFn,
) (*AgentEventStream, error) {
	if err := alValidateContinueContext(agentContext); err != nil {
		return nil, err
	}
	stream := NewAgentEventStream()
	go func() {
		msgs, err := RunAgentLoopContinue(ctx, agentContext, config, func(ev AgentEvent) {
			stream.Push(ev)
		}, streamFn)
		if err != nil {
			stream.End(nil)
			return
		}
		stream.End(msgs)
	}()
	return stream, nil
}

// RunAgentLoop executes an agent loop synchronously, delivering events to emit and returning new messages.
func RunAgentLoop(
	ctx context.Context,
	prompts []AgentMessage,
	agentContext *AgentContext,
	config *AgentLoopConfig,
	emit AgentEventSink,
	streamFn StreamFn,
) ([]AgentMessage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if emit == nil {
		emit = func(AgentEvent) {}
	}
	if agentContext == nil {
		agentContext = &AgentContext{}
	}

	initialMessages := alDeclareToolChanges(agentContext, prompts)
	newMessages := slices.Clone(initialMessages)
	mergedMessages := make([]AgentMessage, 0, len(agentContext.Messages)+len(initialMessages))
	mergedMessages = append(mergedMessages, agentContext.Messages...)
	mergedMessages = append(mergedMessages, initialMessages...)
	currentContext := &AgentContext{
		Messages: mergedMessages,
		Tools:    slices.Clone(agentContext.Tools),
	}

	emit(&AgentEventAgentStart{Type: "agent_start"})
	emit(&AgentEventTurnStart{Type: "turn_start"})
	for _, msg := range initialMessages {
		emit(&AgentEventMessageStart{Type: "message_start", Message: msg})
		emit(&AgentEventMessageEnd{Type: "message_end", Message: msg})
	}

	if err := alRunLoop(ctx, currentContext, &newMessages, config, emit, streamFn); err != nil {
		return nil, err
	}
	return newMessages, nil
}

// RunAgentLoopContinue continues an agent loop synchronously from an existing context.
func RunAgentLoopContinue(
	ctx context.Context,
	agentContext *AgentContext,
	config *AgentLoopConfig,
	emit AgentEventSink,
	streamFn StreamFn,
) ([]AgentMessage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if emit == nil {
		emit = func(AgentEvent) {}
	}
	if err := alValidateContinueContext(agentContext); err != nil {
		return nil, err
	}

	newMessages := make([]AgentMessage, 0)
	currentContext := agentContext.Clone()

	emit(&AgentEventAgentStart{Type: "agent_start"})
	emit(&AgentEventTurnStart{Type: "turn_start"})

	if err := alRunLoop(ctx, currentContext, &newMessages, config, emit, streamFn); err != nil {
		return nil, err
	}
	return newMessages, nil
}

func alValidateContinueContext(agentContext *AgentContext) error {
	if agentContext == nil || len(agentContext.Messages) == 0 {
		return errors.New("Cannot continue: no messages in context")
	}
	last := agentContext.Messages[len(agentContext.Messages)-1]
	if last != nil && last.GetRole() == "assistant" {
		return errors.New("Cannot continue from message role: assistant")
	}
	return nil
}

func alRunLoop(
	ctx context.Context,
	currentContext *AgentContext,
	newMessages *[]AgentMessage,
	initialConfig *AgentLoopConfig,
	emit AgentEventSink,
	streamFn StreamFn,
) error {
	var effectiveConfig AgentLoopConfig
	if initialConfig != nil {
		effectiveConfig = *initialConfig
	}

	var lastCompletedTurn *PrepareNextTurnContext
	explicitContinuation := false

	var pendingMessages []AgentMessage
	if effectiveConfig.GetSteeringMessages != nil {
		var err error
		pendingMessages, err = effectiveConfig.GetSteeringMessages(ctx)
		if err != nil {
			return err
		}
	}

	for {
		hasMoreToolCalls := true

		for hasMoreToolCalls || len(pendingMessages) > 0 {
			var preparedMessages []AgentMessage
			if lastCompletedTurn != nil {
				if effectiveConfig.PrepareNextTurn != nil {
					nextTurnSnapshot, err := effectiveConfig.PrepareNextTurn(ctx, lastCompletedTurn)
					if err != nil {
						return err
					}
					if nextTurnSnapshot != nil {
						if nextTurnSnapshot.Context != nil {
							currentContext = nextTurnSnapshot.Context.Clone()
						}
						if nextTurnSnapshot.Messages != nil {
							preparedMessages = nextTurnSnapshot.Messages
						}
						if nextTurnSnapshot.Model != nil {
							effectiveConfig.Model = nextTurnSnapshot.Model
						}
						if nextTurnSnapshot.ThinkingLevel != nil {
							if *nextTurnSnapshot.ThinkingLevel == ThinkingLevelOff {
								effectiveConfig.Reasoning = nil
							} else {
								tl := *nextTurnSnapshot.ThinkingLevel
								effectiveConfig.Reasoning = &tl
							}
						}
					}
				}
				// Preparation can be long-running (e.g., compaction). Pick up steering queued
				// while it ran, unless an earlier poll already returned steering messages.
				if len(pendingMessages) == 0 && effectiveConfig.GetSteeringMessages != nil {
					var err error
					pendingMessages, err = effectiveConfig.GetSteeringMessages(ctx)
					if err != nil {
						return err
					}
				}
				emit(&AgentEventTurnStart{Type: "turn_start"})
			}

			combined := make([]AgentMessage, 0, len(preparedMessages)+len(pendingMessages))
			combined = append(combined, preparedMessages...)
			combined = append(combined, pendingMessages...)
			for _, message := range alDeclareToolChanges(currentContext, combined) {
				emit(&AgentEventMessageStart{Type: "message_start", Message: message})
				emit(&AgentEventMessageEnd{Type: "message_end", Message: message})
				currentContext.Messages = append(currentContext.Messages, message)
				*newMessages = append(*newMessages, message)
			}
			pendingMessages = nil

			if effectiveConfig.PrepareRequest != nil {
				curThinking := ThinkingLevelOff
				if effectiveConfig.Reasoning != nil {
					curThinking = *effectiveConfig.Reasoning
				}
				requestUpdate, err := effectiveConfig.PrepareRequest(ctx, &PrepareRequestContext{
					Context:       currentContext,
					Model:         effectiveConfig.Model,
					ThinkingLevel: curThinking,
				})
				if err != nil {
					return err
				}
				if requestUpdate != nil {
					if requestUpdate.Context != nil {
						currentContext = requestUpdate.Context.Clone()
					}
					if requestUpdate.Model != nil {
						effectiveConfig.Model = requestUpdate.Model
					}
					if requestUpdate.ThinkingLevel != nil {
						if *requestUpdate.ThinkingLevel == ThinkingLevelOff {
							effectiveConfig.Reasoning = nil
						} else {
							tl := *requestUpdate.ThinkingLevel
							effectiveConfig.Reasoning = &tl
						}
					}
				}
			}

			message, err := alStreamAssistantResponse(ctx, currentContext, &effectiveConfig, emit, streamFn)
			if err != nil {
				return err
			}
			*newMessages = append(*newMessages, message)

			if message.StopReason == ai.StopReasonError || message.StopReason == ai.StopReasonAborted {
				lastCompletedTurn = &AgentTurnContext{
					Message:     message,
					ToolResults: []*ai.ToolResultMessage{},
					Context:     currentContext,
					NewMessages: *newMessages,
				}
				if effectiveConfig.FinishTurn != nil {
					if _, err := effectiveConfig.FinishTurn(ctx, lastCompletedTurn); err != nil {
						return err
					}
				}
				emit(&AgentEventTurnEnd{
					Type:        "turn_end",
					Message:     message,
					ToolResults: []*ai.ToolResultMessage{},
				})
				emit(&AgentEventAgentEnd{
					Type:     "agent_end",
					Messages: *newMessages,
				})
				return nil
			}

			toolCalls := alExtractToolCalls(message)
			toolResults := make([]*ai.ToolResultMessage, 0, len(toolCalls))
			hasMoreToolCalls = false
			if len(toolCalls) > 0 {
				var batch alExecutedToolBatch
				if message.StopReason == ai.StopReasonLength {
					batch = alFailToolCallsFromTruncatedMessage(toolCalls, emit)
				} else {
					batch = alExecuteToolCalls(ctx, currentContext, message, toolCalls, &effectiveConfig, emit)
				}
				toolResults = append(toolResults, batch.messages...)
				hasMoreToolCalls = !batch.terminate
				for _, tr := range toolResults {
					currentContext.Messages = append(currentContext.Messages, tr)
					*newMessages = append(*newMessages, tr)
				}
			}

			lastCompletedTurn = &AgentTurnContext{
				Message:     message,
				ToolResults: toolResults,
				Context:     currentContext,
				NewMessages: *newMessages,
			}
			var decision *AgentTurnDecision
			if effectiveConfig.FinishTurn != nil {
				decision, err = effectiveConfig.FinishTurn(ctx, lastCompletedTurn)
				if err != nil {
					return err
				}
			}
			emit(&AgentEventTurnEnd{
				Type:        "turn_end",
				Message:     message,
				ToolResults: toolResults,
			})

			if decision != nil && decision.Action == AgentTurnDecisionEnd {
				emit(&AgentEventAgentEnd{
					Type:     "agent_end",
					Messages: *newMessages,
				})
				return nil
			}

			explicitContinuation = decision != nil && decision.Action == AgentTurnDecisionContinue
			if effectiveConfig.GetSteeringMessages != nil {
				pendingMessages, err = effectiveConfig.GetSteeringMessages(ctx)
				if err != nil {
					return err
				}
			} else {
				pendingMessages = nil
			}
			if hasMoreToolCalls || len(pendingMessages) > 0 {
				explicitContinuation = false
			}
		}

		var followUpMessages []AgentMessage
		if effectiveConfig.GetFollowUpMessages != nil {
			var err error
			followUpMessages, err = effectiveConfig.GetFollowUpMessages(ctx)
			if err != nil {
				return err
			}
		}
		if len(followUpMessages) > 0 {
			explicitContinuation = false
			pendingMessages = followUpMessages
			continue
		}

		if explicitContinuation {
			explicitContinuation = false
			continue
		}

		break
	}

	emit(&AgentEventAgentEnd{
		Type:     "agent_end",
		Messages: *newMessages,
	})
	return nil
}

// alDeclareToolChanges reconciles context.Tools against system messages in context.Messages
// and pendingMessages, rewriting an existing pending system message or inserting a new one
// before the first non-system pending message.
func alDeclareToolChanges(agentContext *AgentContext, pendingMessages []AgentMessage) []AgentMessage {
	systemIndex := -1
	for i := len(pendingMessages) - 1; i >= 0; i-- {
		if pendingMessages[i] != nil && pendingMessages[i].GetRole() == "system" {
			systemIndex = i
			break
		}
	}

	var pendingSys *ai.SystemMessage
	if systemIndex >= 0 {
		if sys, ok := pendingMessages[systemIndex].(*ai.SystemMessage); ok && sys != nil {
			pendingSys = sys
		} else if conv := DefaultConvertToLlm([]AgentMessage{pendingMessages[systemIndex]}); len(conv) > 0 {
			if sys, ok := conv[0].(*ai.SystemMessage); ok {
				pendingSys = sys
			}
		}
	}

	baseline := pendingMessages
	if pendingSys != nil {
		baseline = make([]AgentMessage, len(pendingMessages))
		copy(baseline, pendingMessages)
		baseline[systemIndex] = alWithToolChanges(pendingSys, ai.ToolStateChanges{})
	}

	combinedForTools := make([]AgentMessage, 0, len(agentContext.Messages)+len(baseline))
	combinedForTools = append(combinedForTools, agentContext.Messages...)
	combinedForTools = append(combinedForTools, baseline...)

	changes := ai.GetToolStateChanges(
		ai.GetCurrentTools(DefaultConvertToLlm(combinedForTools)),
		alDeclaredAiTools(agentContext.Tools),
	)
	unchanged := len(changes.ToolsAdded) == 0 && len(changes.ToolsRemoved) == 0

	if pendingSys != nil {
		if unchanged && len(pendingSys.ToolsAdded) == 0 && len(pendingSys.ToolsRemoved) == 0 {
			return pendingMessages
		}
		out := make([]AgentMessage, len(baseline))
		copy(out, baseline)
		out[systemIndex] = alWithToolChanges(pendingSys, changes)
		return out
	}

	if unchanged {
		return pendingMessages
	}

	empty := ""
	update := alWithToolChanges(&ai.SystemMessage{
		Role:      "system",
		Content:   ai.SystemMessageContent{Text: &empty},
		Timestamp: time.Now().UnixMilli(),
	}, changes)

	insertIndex := len(pendingMessages)
	for i, msg := range pendingMessages {
		if msg == nil || msg.GetRole() != "system" {
			insertIndex = i
			break
		}
	}
	out := make([]AgentMessage, 0, len(pendingMessages)+1)
	out = append(out, pendingMessages[:insertIndex]...)
	out = append(out, update)
	out = append(out, pendingMessages[insertIndex:]...)
	return out
}

func alWithToolChanges(msg *ai.SystemMessage, changes ai.ToolStateChanges) *ai.SystemMessage {
	if msg == nil {
		return nil
	}
	cp := *msg
	if len(changes.ToolsAdded) > 0 {
		cp.ToolsAdded = slices.Clone(changes.ToolsAdded)
	} else {
		cp.ToolsAdded = nil
	}
	if len(changes.ToolsRemoved) > 0 {
		cp.ToolsRemoved = slices.Clone(changes.ToolsRemoved)
	} else {
		cp.ToolsRemoved = nil
	}
	return &cp
}

func alDeclaredAiTools(tools []*AgentTool) []*ai.Tool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]*ai.Tool, 0, len(tools))
	for _, t := range tools {
		if t != nil {
			out = append(out, ai.ToToolDeclaration(t.AiTool()))
		}
	}
	return out
}

func alStreamAssistantResponse(
	ctx context.Context,
	currentContext *AgentContext,
	effectiveConfig *AgentLoopConfig,
	emit AgentEventSink,
	streamFn StreamFn,
) (*ai.AssistantMessage, error) {
	messages := slices.Clone(currentContext.Messages)
	if effectiveConfig.TransformContext != nil {
		var err error
		messages, err = effectiveConfig.TransformContext(ctx, messages)
		if err != nil {
			return nil, err
		}
	}

	var llmMessages []ai.Message
	if effectiveConfig.ConvertToLlm != nil {
		var err error
		llmMessages, err = effectiveConfig.ConvertToLlm(ctx, messages)
		if err != nil {
			return nil, err
		}
	} else {
		llmMessages = DefaultConvertToLlm(messages)
	}

	llmContext := ai.NormalizeContext(&ai.Context{Messages: llmMessages})

	streamOptions, err := alBuildSimpleStreamOptions(ctx, effectiveConfig, effectiveConfig.Model)
	if err != nil {
		return nil, err
	}

	fn := streamFn
	if fn == nil {
		fn = GetDefaultStreamFn()
	}

	stream, err := fn(ctx, effectiveConfig.Model, llmContext, streamOptions)
	if err != nil {
		return nil, err
	}
	if stream == nil {
		return nil, errors.New("streamFn returned nil stream")
	}

	requestedThinkingLevel := ThinkingLevelOff
	if effectiveConfig.Reasoning != nil {
		requestedThinkingLevel = *effectiveConfig.Reasoning
	}
	applyThinkingLevel := func(msg *ai.AssistantMessage) {
		if msg == nil {
			return
		}
		tl := requestedThinkingLevel
		msg.ThinkingLevel = &tl
	}

	var partialMessage *ai.AssistantMessage
	addedPartial := false

	for {
		event, ok, nextErr := stream.Next(ctx)
		if nextErr != nil || !ok {
			break
		}
		switch ev := event.(type) {
		case *ai.AssistantMessageEventStart:
			partialMessage = ev.Partial
			currentContext.Messages = append(currentContext.Messages, partialMessage)
			addedPartial = true
			emit(&AgentEventMessageStart{
				Type:    "message_start",
				Message: proxyCloneAssistantMessage(partialMessage),
			})

		case *ai.AssistantMessageEventTextStart,
			*ai.AssistantMessageEventTextDelta,
			*ai.AssistantMessageEventTextEnd,
			*ai.AssistantMessageEventThinkingStart,
			*ai.AssistantMessageEventThinkingDelta,
			*ai.AssistantMessageEventThinkingEnd,
			*ai.AssistantMessageEventToolcallStart,
			*ai.AssistantMessageEventToolcallDelta,
			*ai.AssistantMessageEventToolcallEnd:
			if partialMessage != nil {
				if partial := alEventPartial(ev); partial != nil {
					partialMessage = partial
					if addedPartial && len(currentContext.Messages) > 0 {
						currentContext.Messages[len(currentContext.Messages)-1] = partialMessage
					}
					emit(&AgentEventMessageUpdate{
						Type:                  "message_update",
						Message:               proxyCloneAssistantMessage(partialMessage),
						AssistantMessageEvent: ev,
					})
				}
			}

		case *ai.AssistantMessageEventDone, *ai.AssistantMessageEventError:
			finalMessage, resErr := stream.Result(ctx)
			if resErr != nil {
				return nil, resErr
			}
			if finalMessage == nil {
				if e, ok := ev.(*ai.AssistantMessageEventDone); ok {
					finalMessage = e.Message
				} else if e, ok := ev.(*ai.AssistantMessageEventError); ok {
					finalMessage = e.Error
				}
			}
			if finalMessage == nil {
				return nil, errors.New("stream completed without final assistant message")
			}
			applyThinkingLevel(finalMessage)
			if addedPartial && len(currentContext.Messages) > 0 {
				currentContext.Messages[len(currentContext.Messages)-1] = finalMessage
			} else {
				currentContext.Messages = append(currentContext.Messages, finalMessage)
				emit(&AgentEventMessageStart{
					Type:    "message_start",
					Message: proxyCloneAssistantMessage(finalMessage),
				})
			}
			emit(&AgentEventMessageEnd{
				Type:    "message_end",
				Message: finalMessage,
			})
			return finalMessage, nil
		}
	}

	finalMessage, err := stream.Result(ctx)
	if finalMessage == nil && addedPartial && partialMessage != nil {
		snap := proxyCloneAssistantMessage(partialMessage)
		if ctx.Err() != nil {
			snap.StopReason = ai.StopReasonAborted
			msg := ctx.Err().Error()
			snap.ErrorMessage = &msg
		} else if err != nil {
			snap.StopReason = ai.StopReasonError
			msg := err.Error()
			snap.ErrorMessage = &msg
		} else {
			snap.StopReason = ai.StopReasonError
			msg := "stream ended unexpectedly without done or error event"
			snap.ErrorMessage = &msg
		}
		applyThinkingLevel(snap)
		currentContext.Messages[len(currentContext.Messages)-1] = snap
		emit(&AgentEventMessageEnd{
			Type:    "message_end",
			Message: snap,
		})
		return snap, nil
	}
	if err != nil {
		return nil, err
	}
	if finalMessage == nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("stream ended without assistant message")
	}

	applyThinkingLevel(finalMessage)
	if addedPartial && len(currentContext.Messages) > 0 {
		currentContext.Messages[len(currentContext.Messages)-1] = finalMessage
	} else {
		currentContext.Messages = append(currentContext.Messages, finalMessage)
		emit(&AgentEventMessageStart{
			Type:    "message_start",
			Message: proxyCloneAssistantMessage(finalMessage),
		})
	}
	emit(&AgentEventMessageEnd{
		Type:    "message_end",
		Message: finalMessage,
	})
	return finalMessage, nil
}

func alBuildSimpleStreamOptions(
	ctx context.Context,
	cfg *AgentLoopConfig,
	model *ai.Model,
) (*ai.SimpleStreamOptions, error) {
	apiKey := cfg.ApiKey
	if cfg.GetApiKey != nil && model != nil {
		resolved, err := cfg.GetApiKey(ctx, string(model.Provider))
		if err != nil {
			return nil, err
		}
		if resolved != "" {
			apiKey = &resolved
		}
	}

	var reasoning *ai.ThinkingLevel
	if cfg.Reasoning != nil && *cfg.Reasoning != ThinkingLevelOff {
		r := ai.ThinkingLevel(*cfg.Reasoning)
		reasoning = &r
	}

	return &ai.SimpleStreamOptions{
		StreamOptions: ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
				TelemetryContext: cfg.TelemetryContext,
				ApiKey:           apiKey,
				HTTPClient:       cfg.HTTPClient,
				Env:              cfg.Env,
				OnPayload:        cfg.OnPayload,
				OnResponse:       cfg.OnResponse,
				Headers:          cfg.Headers,
				Identity:         ai.MergeClientIdentity(DefaultAgentIdentity, cfg.Identity),
				TimeoutMs:        cfg.TimeoutMs,
				MaxRetries:       cfg.MaxRetries,
				MaxRetryDelayMs:  cfg.MaxRetryDelayMs,
			},
			OnProviderStreamEvent:     cfg.OnProviderStreamEvent,
			Temperature:               cfg.Temperature,
			SamplingParams:            cfg.SamplingParams,
			MaxTokens:                 cfg.MaxTokens,
			Transport:                 cfg.Transport,
			CacheRetention:            cfg.CacheRetention,
			SessionId:                 cfg.SessionId,
			DisableContinuation:       cfg.DisableContinuation,
			WebsocketConnectTimeoutMs: cfg.WebsocketConnectTimeoutMs,
			Metadata:                  cfg.Metadata,
		},
		Reasoning:       reasoning,
		ThinkingBudgets: cfg.ThinkingBudgets,
	}, nil
}

func alEventPartial(ev ai.AssistantMessageEvent) *ai.AssistantMessage {
	switch e := ev.(type) {
	case *ai.AssistantMessageEventStart:
		return e.Partial
	case *ai.AssistantMessageEventTextStart:
		return e.Partial
	case *ai.AssistantMessageEventTextDelta:
		return e.Partial
	case *ai.AssistantMessageEventTextEnd:
		return e.Partial
	case *ai.AssistantMessageEventThinkingStart:
		return e.Partial
	case *ai.AssistantMessageEventThinkingDelta:
		return e.Partial
	case *ai.AssistantMessageEventThinkingEnd:
		return e.Partial
	case *ai.AssistantMessageEventToolcallStart:
		return e.Partial
	case *ai.AssistantMessageEventToolcallDelta:
		return e.Partial
	case *ai.AssistantMessageEventToolcallEnd:
		return e.Partial
	default:
		return nil
	}
}

func alExtractToolCalls(msg *ai.AssistantMessage) []*AgentToolCall {
	if msg == nil {
		return nil
	}
	var calls []*AgentToolCall
	for _, block := range msg.Content {
		if tc, ok := block.(*ai.ToolCall); ok && tc != nil {
			calls = append(calls, alAiToolCallToAgent(tc))
		}
	}
	return calls
}

type alExecutedToolBatch struct {
	messages  []*ai.ToolResultMessage
	terminate bool
}

func alFailToolCallsFromTruncatedMessage(
	toolCalls []*AgentToolCall,
	emit AgentEventSink,
) alExecutedToolBatch {
	results := make([]*ai.ToolResultMessage, 0, len(toolCalls))
	for _, tc := range toolCalls {
		emit(&AgentEventToolExecutionStart{
			Type:       "tool_execution_start",
			ToolCallId: tc.Id,
			ToolName:   tc.Name,
			Args:       tc.Arguments,
		})
		errText := fmt.Sprintf(
			"Tool call %q was not executed: the response hit the output token limit, so its arguments may be truncated. Re-issue the tool call with complete arguments.",
			tc.Name,
		)
		finalized := AgentToolCallOutcome{
			ToolCall: tc,
			Result:   alErrorToolResult(errText),
			IsError:  true,
		}
		alEmitToolExecutionEnd(finalized, emit)
		tr := alCreateToolResultMessage(finalized)
		results = append(results, tr)
		alEmitToolResultMessage(tr, emit)
	}
	return alExecutedToolBatch{
		messages:  results,
		terminate: false,
	}
}

func alExecuteToolCalls(
	ctx context.Context,
	agentContext *AgentContext,
	assistantMessage *ai.AssistantMessage,
	toolCalls []*AgentToolCall,
	effectiveConfig *AgentLoopConfig,
	emit AgentEventSink,
) alExecutedToolBatch {
	hooks := ToolCallHooks{
		BeforeToolCall: effectiveConfig.BeforeToolCall,
		AfterToolCall:  effectiveConfig.AfterToolCall,
	}
	hasSequentialToolCall := false
	for _, tc := range toolCalls {
		for _, tool := range agentContext.Tools {
			if tool != nil && tool.Name == tc.Name && tool.ExecutionMode == ToolExecutionModeSequential {
				hasSequentialToolCall = true
				break
			}
		}
		if hasSequentialToolCall {
			break
		}
	}
	if effectiveConfig.ToolExecution == ToolExecutionModeSequential || hasSequentialToolCall {
		return alExecuteToolCallsSequential(ctx, agentContext, assistantMessage, toolCalls, hooks, emit)
	}
	return alExecuteToolCallsParallel(ctx, agentContext, assistantMessage, toolCalls, hooks, emit)
}

func alExecuteToolCallsSequential(
	ctx context.Context,
	agentContext *AgentContext,
	assistantMessage *ai.AssistantMessage,
	toolCalls []*AgentToolCall,
	hooks ToolCallHooks,
	emit AgentEventSink,
) alExecutedToolBatch {
	finalizedCalls := make([]AgentToolCallOutcome, 0, len(toolCalls))
	messages := make([]*ai.ToolResultMessage, 0, len(toolCalls))

	for _, tc := range toolCalls {
		emit(&AgentEventToolExecutionStart{
			Type:       "tool_execution_start",
			ToolCallId: tc.Id,
			ToolName:   tc.Name,
			Args:       tc.Arguments,
		})

		prep := alPrepareToolCall(ctx, agentContext, assistantMessage, tc, hooks, agentContext.Tools)
		var finalized AgentToolCallOutcome
		if prep.immediate != nil {
			finalized = *prep.immediate
		} else {
			executed := alExecutePreparedToolCall(ctx, prep, alEmitToolUpdateSink(tc, emit))
			finalized = alFinalizeExecutedToolCall(ctx, agentContext, assistantMessage, prep, executed, hooks)
		}

		alEmitToolExecutionEnd(finalized, emit)
		tr := alCreateToolResultMessage(finalized)
		alEmitToolResultMessage(tr, emit)
		finalizedCalls = append(finalizedCalls, finalized)
		messages = append(messages, tr)
	}

	return alExecutedToolBatch{
		messages:  messages,
		terminate: alShouldTerminateToolBatch(finalizedCalls),
	}
}

func alExecuteToolCallsParallel(
	ctx context.Context,
	agentContext *AgentContext,
	assistantMessage *ai.AssistantMessage,
	toolCalls []*AgentToolCall,
	hooks ToolCallHooks,
	emit AgentEventSink,
) alExecutedToolBatch {
	var emitMu sync.Mutex
	safeEmit := func(ev AgentEvent) {
		emitMu.Lock()
		defer emitMu.Unlock()
		emit(ev)
	}

	type queuedCall struct {
		index int
		tc    *AgentToolCall
		prep  alPreparedToolCall
	}

	outcomes := make([]AgentToolCallOutcome, len(toolCalls))
	var queued []queuedCall

	// Prepare calls in source order, emitting tool_execution_start for every call
	// and resolving immediate aborts/errors without leaving calls unanswered (#9986).
	for i, tc := range toolCalls {
		safeEmit(&AgentEventToolExecutionStart{
			Type:       "tool_execution_start",
			ToolCallId: tc.Id,
			ToolName:   tc.Name,
			Args:       tc.Arguments,
		})

		prep := alPrepareToolCall(ctx, agentContext, assistantMessage, tc, hooks, agentContext.Tools)
		if prep.immediate != nil {
			outcomes[i] = *prep.immediate
			alEmitToolExecutionEnd(*prep.immediate, safeEmit)
			continue
		}
		queued = append(queued, queuedCall{index: i, tc: tc, prep: prep})
	}

	if len(queued) > 0 {
		var wg sync.WaitGroup
		wg.Add(len(queued))
		for _, item := range queued {
			go func(q queuedCall) {
				defer wg.Done()
				var finalized AgentToolCallOutcome
				if ctx.Err() != nil {
					finalized = AgentToolCallOutcome{
						ToolCall: q.tc,
						Result:   alErrorToolResult("Operation aborted"),
						IsError:  true,
					}
				} else {
					executed := alExecutePreparedToolCall(ctx, q.prep, alEmitToolUpdateSink(q.tc, safeEmit))
					finalized = alFinalizeExecutedToolCall(ctx, agentContext, assistantMessage, q.prep, executed, hooks)
				}
				outcomes[q.index] = finalized
				alEmitToolExecutionEnd(finalized, safeEmit)
			}(item)
		}
		wg.Wait()
	}

	messages := make([]*ai.ToolResultMessage, 0, len(toolCalls))
	for _, finalized := range outcomes {
		tr := alCreateToolResultMessage(finalized)
		alEmitToolResultMessage(tr, emit)
		messages = append(messages, tr)
	}

	return alExecutedToolBatch{
		messages:  messages,
		terminate: alShouldTerminateToolBatch(outcomes),
	}
}

func alShouldTerminateToolBatch(finalizedCalls []AgentToolCallOutcome) bool {
	if len(finalizedCalls) == 0 {
		return false
	}
	for _, finalized := range finalizedCalls {
		if finalized.Result.Terminate == nil || !*finalized.Result.Terminate {
			return false
		}
	}
	return true
}

// RunToolCall executes a single tool call through argument preparation, schema validation,
// beforeToolCall, execution, and afterToolCall without emitting loop events or modifying transcripts.
func RunToolCall(
	ctx context.Context,
	toolCall *AgentToolCall,
	options *RunToolCallOptions,
) AgentToolCallOutcome {
	if ctx == nil {
		ctx = context.Background()
	}
	if toolCall == nil {
		return AgentToolCallOutcome{
			Result:  alErrorToolResult("toolCall is nil"),
			IsError: true,
		}
	}
	var opts RunToolCallOptions
	if options != nil {
		opts = *options
	}
	agentContext := opts.Context
	if agentContext == nil {
		agentContext = &AgentContext{Tools: slices.Clone(opts.Tools)}
	}

	prep := alPrepareToolCall(ctx, agentContext, opts.AssistantMessage, toolCall, opts.ToolCallHooks, opts.Tools)
	if prep.immediate != nil {
		return *prep.immediate
	}
	executed := alExecutePreparedToolCall(ctx, prep, opts.OnUpdate)
	return alFinalizeExecutedToolCall(ctx, agentContext, opts.AssistantMessage, prep, executed, opts.ToolCallHooks)
}

type alPreparedToolCall struct {
	toolCall      *AgentToolCall
	tool          *AgentTool
	validatedArgs any
	immediate     *AgentToolCallOutcome
}

type alExecutedToolCallOutcome struct {
	result     AgentToolResult
	isError    bool
	durationMs int64
}

func alPrepareToolCall(
	ctx context.Context,
	agentContext *AgentContext,
	assistantMessage *ai.AssistantMessage,
	toolCall *AgentToolCall,
	hooks ToolCallHooks,
	tools []*AgentTool,
) alPreparedToolCall {
	// Check abort signal FIRST (#9986) before tool lookup or beforeToolCall.
	if ctx.Err() != nil {
		return alPreparedToolCall{
			immediate: &AgentToolCallOutcome{
				ToolCall: toolCall,
				Result:   alErrorToolResult("Operation aborted"),
				IsError:  true,
			},
		}
	}

	var tool *AgentTool
	for _, t := range tools {
		if t != nil && t.Name == toolCall.Name {
			tool = t
			break
		}
	}
	if tool == nil {
		return alPreparedToolCall{
			immediate: &AgentToolCallOutcome{
				ToolCall: toolCall,
				Result:   alErrorToolResult(fmt.Sprintf("Tool %s not found", toolCall.Name)),
				IsError:  true,
			},
		}
	}

	preparedCall := alPrepareToolCallArguments(tool, toolCall)
	validatedArgs, err := ai.ValidateToolArguments(tool.AiTool(), alAgentToolCallToAi(preparedCall))
	if err != nil {
		return alPreparedToolCall{
			immediate: &AgentToolCallOutcome{
				ToolCall: toolCall,
				Result:   alErrorToolResult(err.Error()),
				IsError:  true,
			},
		}
	}

	if hooks.BeforeToolCall != nil {
		beforeRes, err := hooks.BeforeToolCall(ctx, &BeforeToolCallContext{
			AssistantMessage: assistantMessage,
			ToolCall:         toolCall,
			Args:             validatedArgs,
			Context:          agentContext,
		})
		if err != nil {
			return alPreparedToolCall{
				immediate: &AgentToolCallOutcome{
					ToolCall: toolCall,
					Result:   alErrorToolResult(err.Error()),
					IsError:  true,
				},
			}
		}
		if ctx.Err() != nil {
			return alPreparedToolCall{
				immediate: &AgentToolCallOutcome{
					ToolCall: toolCall,
					Result:   alErrorToolResult("Operation aborted"),
					IsError:  true,
				},
			}
		}
		if beforeRes != nil && beforeRes.Block {
			reason := beforeRes.Reason
			if reason == "" {
				reason = "Tool execution was blocked"
			}
			res := alErrorToolResult(reason)
			if beforeRes.Terminate {
				t := true
				res.Terminate = &t
			}
			return alPreparedToolCall{
				immediate: &AgentToolCallOutcome{
					ToolCall: toolCall,
					Result:   res,
					IsError:  true,
				},
			}
		}
	}

	return alPreparedToolCall{
		toolCall:      toolCall,
		tool:          tool,
		validatedArgs: validatedArgs,
	}
}

func alPrepareToolCallArguments(tool *AgentTool, toolCall *AgentToolCall) *AgentToolCall {
	if tool == nil || tool.PrepareArguments == nil {
		return toolCall
	}
	rawArgs := toolCall.Arguments
	if rawArgs == nil {
		rawArgs = jsonx.NewObject()
	}
	prepared := tool.PrepareArguments(rawArgs)
	cp := *toolCall
	cp.Arguments = prepared
	return &cp
}

func alExecutePreparedToolCall(
	ctx context.Context,
	prep alPreparedToolCall,
	onUpdate AgentToolUpdateCallback,
) alExecutedToolCallOutcome {
	start := time.Now()

	var updateMu sync.Mutex
	var updateWg sync.WaitGroup
	acceptingUpdates := true

	wrappedOnUpdate := func(AgentToolResult) {}
	if onUpdate != nil {
		wrappedOnUpdate = func(partialResult AgentToolResult) {
			updateMu.Lock()
			if !acceptingUpdates {
				updateMu.Unlock()
				return
			}
			updateWg.Add(1)
			updateMu.Unlock()
			defer updateWg.Done()

			onUpdate(partialResult)
		}
	}

	var result AgentToolResult
	var execErr error
	if prep.tool.Execute == nil {
		execErr = fmt.Errorf("tool %s has nil Execute handler", prep.tool.Name)
	} else {
		result, execErr = prep.tool.Execute(ctx, prep.toolCall.Id, prep.validatedArgs, wrappedOnUpdate)
	}

	// Stop accepting late updates and wait for in-flight update callbacks before returning.
	updateMu.Lock()
	acceptingUpdates = false
	updateMu.Unlock()
	updateWg.Wait()

	durationMs := int64(math.Max(0, math.Round(time.Since(start).Seconds()*1000)))
	if execErr != nil {
		return alExecutedToolCallOutcome{
			result:     alErrorToolResult(execErr.Error()),
			isError:    true,
			durationMs: durationMs,
		}
	}
	return alExecutedToolCallOutcome{
		result:     result,
		isError:    result.IsError != nil && *result.IsError,
		durationMs: durationMs,
	}
}

func alFinalizeExecutedToolCall(
	ctx context.Context,
	agentContext *AgentContext,
	assistantMessage *ai.AssistantMessage,
	prep alPreparedToolCall,
	executed alExecutedToolCallOutcome,
	hooks ToolCallHooks,
) AgentToolCallOutcome {
	res := executed.result
	isError := executed.isError
	dur := executed.durationMs

	if hooks.AfterToolCall != nil {
		hookResultCopy := res
		afterRes, err := hooks.AfterToolCall(ctx, &AfterToolCallContext{
			AssistantMessage: assistantMessage,
			ToolCall:         prep.toolCall,
			Args:             prep.validatedArgs,
			Result:           &hookResultCopy,
			IsError:          isError,
			Context:          agentContext,
		})
		if err != nil {
			return AgentToolCallOutcome{
				ToolCall:   prep.toolCall,
				Result:     alErrorToolResult(err.Error()),
				IsError:    true,
				DurationMs: &dur,
			}
		}
		if afterRes != nil {
			// Drop existing structuredContent if content is replaced without a new structuredContent.
			hasNewSC, newSC := false, any(nil)
			if v, ok := afterRes.StructuredContent.Get(); ok {
				hasNewSC = true
				newSC = v
			}

			if afterRes.Content != nil {
				res.Content = afterRes.Content
			}
			if v, ok := afterRes.Details.Get(); ok {
				res.Details = v
			}
			if afterRes.Usage != nil {
				res.Usage = afterRes.Usage
			}
			if afterRes.Terminate != nil {
				res.Terminate = afterRes.Terminate
			}
			if hasNewSC {
				res.StructuredContent = newSC
			} else if afterRes.Content != nil {
				res.StructuredContent = nil
			}
			if afterRes.IsError != nil {
				isError = *afterRes.IsError
			}
		}
	}

	return AgentToolCallOutcome{
		ToolCall:   prep.toolCall,
		Result:     res,
		IsError:    isError,
		DurationMs: &dur,
	}
}

func alErrorToolResult(message string) AgentToolResult {
	return AgentToolResult{
		Content: alTextContentList(message),
		Details: jsonx.NewObject(),
	}
}

func alEmitToolUpdateSink(tc *AgentToolCall, emit AgentEventSink) AgentToolUpdateCallback {
	if emit == nil {
		return nil
	}
	return func(partialResult AgentToolResult) {
		emit(&AgentEventToolExecutionUpdate{
			Type:          "tool_execution_update",
			ToolCallId:    tc.Id,
			ToolName:      tc.Name,
			Args:          tc.Arguments,
			PartialResult: partialResult,
		})
	}
}

func alEmitToolExecutionEnd(finalized AgentToolCallOutcome, emit AgentEventSink) {
	if emit == nil {
		return
	}
	resCopy := finalized.Result
	emit(&AgentEventToolExecutionEnd{
		Type:       "tool_execution_end",
		ToolCallId: finalized.ToolCall.Id,
		ToolName:   finalized.ToolCall.Name,
		Result:     &resCopy,
		IsError:    finalized.IsError,
		DurationMs: finalized.DurationMs,
	})
}

func alCreateToolResultMessage(finalized AgentToolCallOutcome) *ai.ToolResultMessage {
	content := finalized.Result.Content
	if content == nil {
		content = ai.ContentList{}
	}
	var detailsOpt jsonx.Opt[any]
	if finalized.Result.Details != nil {
		detailsOpt = jsonx.Some(finalized.Result.Details)
	}
	return &ai.ToolResultMessage{
		Role:       "toolResult",
		ToolCallId: finalized.ToolCall.Id,
		ToolName:   finalized.ToolCall.Name,
		Content:    content,
		Details:    detailsOpt,
		Usage:      finalized.Result.Usage,
		IsError:    finalized.IsError,
		DurationMs: finalized.DurationMs,
		Timestamp:  time.Now().UnixMilli(),
	}
}

func alEmitToolResultMessage(tr *ai.ToolResultMessage, emit AgentEventSink) {
	if emit == nil {
		return
	}
	emit(&AgentEventMessageStart{Type: "message_start", Message: tr})
	emit(&AgentEventMessageEnd{Type: "message_end", Message: tr})
}

func alAiToolCallToAgent(tc *ai.ToolCall) *AgentToolCall {
	if tc == nil {
		return nil
	}
	return &AgentToolCall{
		Type:             tc.Type,
		Id:               tc.Id,
		Name:             tc.Name,
		Arguments:        tc.Arguments,
		ThoughtSignature: tc.ThoughtSignature,
		Namespace:        tc.Namespace,
		ArgumentsError:   tc.ArgumentsError,
	}
}

func alAgentToolCallToAi(tc *AgentToolCall) *ai.ToolCall {
	if tc == nil {
		return nil
	}
	var argsObj *jsonx.Object
	switch v := tc.Arguments.(type) {
	case *jsonx.Object:
		argsObj = v
	case nil:
		argsObj = nil
	default:
		if b, err := jsonx.Marshal(v); err == nil {
			if parsed, err := jsonx.Parse(b); err == nil {
				if obj, ok := parsed.(*jsonx.Object); ok {
					argsObj = obj
				}
			}
		}
	}
	return &ai.ToolCall{
		Type:             tc.Type,
		Id:               tc.Id,
		Name:             tc.Name,
		Arguments:        argsObj,
		ThoughtSignature: tc.ThoughtSignature,
		Namespace:        tc.Namespace,
		ArgumentsError:   tc.ArgumentsError,
	}
}

func alTextContentList(text string) ai.ContentList {
	return ai.ContentList{
		&ai.TextContent{
			Type: "text",
			Text: text,
		},
	}
}

func alAiToolsFromAgentTools(tools []*AgentTool) []*ai.Tool {
	if tools == nil {
		return nil
	}
	out := make([]*ai.Tool, 0, len(tools))
	for _, t := range tools {
		if t != nil {
			out = append(out, t.AiTool())
		}
	}
	return out
}

func alToAiMessages(msgs []AgentMessage) []ai.Message {
	out := make([]ai.Message, 0, len(msgs))
	for _, m := range msgs {
		if aiMsg, ok := m.(ai.Message); ok && aiMsg != nil {
			out = append(out, aiMsg)
		}
	}
	return out
}

func alAiMessagesToAgentMessages(msgs []ai.Message) []AgentMessage {
	if msgs == nil {
		return nil
	}
	out := make([]AgentMessage, len(msgs))
	for i, m := range msgs {
		out[i] = m
	}
	return out
}

// alReplaceSystemPromptInMessages replaces the text of the current/initial system message in-place
// with a cloned message carrying newPrompt. Only used by Agent.SetSystemPrompt.
func alReplaceSystemPromptInMessages(msgs []AgentMessage, newPrompt string) bool {
	for i := len(msgs) - 1; i >= 0; i-- {
		sm, ok := msgs[i].(*ai.SystemMessage)
		if !ok || sm == nil {
			continue
		}
		if sm.Content.Text != nil || len(sm.Content.Blocks) > 0 || i == 0 {
			cp := *sm
			sp := newPrompt
			cp.Content = ai.SystemMessageContent{Text: &sp}
			msgs[i] = &cp
			return true
		}
	}
	return false
}
