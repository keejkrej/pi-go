// Ported from packages/agent/test/agent-loop.test.ts (pi v1.5.2).

package agent_test

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/agent/internal/testkit"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

type talCustomArtifactMessage struct {
	Role string `json:"role"`
	Note string `json:"note"`
}

func (m *talCustomArtifactMessage) GetRole() string { return m.Role }

func talIdentityConvertToLlm(_ context.Context, msgs []agent.AgentMessage) ([]ai.Message, error) {
	return agent.DefaultConvertToLlm(msgs), nil
}

func talMockModel() *ai.Model {
	return &ai.Model{
		BaseModel: ai.BaseModel{
			Id:       "mock-model",
			Name:     "Mock Model",
			Api:      string(ai.KnownApiOpenaiCompletions),
			Provider: ai.KnownProviderOpenai,
			BaseUrl:  "https://example.invalid",
			Input:    []ai.ModelModality{ai.ModelModalityText},
		},
		Reasoning:     true,
		ContextWindow: 128000,
		MaxTokens:     4096,
	}
}

func talCreateAssistantMessage(content ai.ContentList, stopReason ai.StopReason, errMsg ...string) *ai.AssistantMessage {
	var e *string
	if len(errMsg) > 0 && errMsg[0] != "" {
		s := errMsg[0]
		e = &s
	}
	return &ai.AssistantMessage{
		Role:         "assistant",
		Content:      content,
		Api:          ai.KnownApiOpenaiCompletions,
		Provider:     ai.KnownProviderOpenai,
		Model:        "mock-model",
		StopReason:   stopReason,
		ErrorMessage: e,
		Timestamp:    time.Now().UnixMilli(),
	}
}

func talCreateUserMessage(text string) *ai.UserMessage {
	return &ai.UserMessage{
		Role: "user",
		Content: ai.UserMessageContent{
			Blocks: ai.ContentList{&ai.TextContent{Type: "text", Text: text}},
		},
		Timestamp: time.Now().UnixMilli(),
	}
}

func talEventTypes(events []agent.AgentEvent) []string {
	out := make([]string, 0, len(events))
	for _, ev := range events {
		switch e := ev.(type) {
		case *agent.AgentEventAgentStart:
			out = append(out, "agent_start")
		case *agent.AgentEventAgentEnd:
			out = append(out, "agent_end")
		case *agent.AgentEventTurnStart:
			out = append(out, "turn_start")
		case *agent.AgentEventTurnEnd:
			out = append(out, "turn_end")
		case *agent.AgentEventMessageStart:
			out = append(out, "message_start:"+e.Message.GetRole())
		case *agent.AgentEventMessageUpdate:
			out = append(out, "message_update")
		case *agent.AgentEventMessageEnd:
			out = append(out, "message_end:"+e.Message.GetRole())
		case *agent.AgentEventToolExecutionStart:
			out = append(out, "tool_start:"+e.ToolCallId)
		case *agent.AgentEventToolExecutionUpdate:
			out = append(out, "tool_update:"+e.ToolCallId)
		case *agent.AgentEventToolExecutionEnd:
			out = append(out, "tool_end:"+e.ToolCallId)
		}
	}
	return out
}

func talSimpleSchema(propNames ...string) *typebox.Schema {
	props := omap.NewMap[string, *typebox.Schema]()
	for _, name := range propNames {
		props.Set(name, typebox.String())
	}
	return typebox.Object(props)
}

func talPushAssistantDone(msg *ai.AssistantMessage) *ai.AssistantMessageEventStream {
	s := ai.NewAssistantMessageEventStream()
	partial := &ai.AssistantMessage{
		Role:       "assistant",
		Content:    ai.ContentList{},
		Api:        msg.Api,
		Provider:   msg.Provider,
		Model:      msg.Model,
		StopReason: ai.StopReasonStop,
		Timestamp:  msg.Timestamp,
	}
	s.Push(&ai.AssistantMessageEventStart{Type: "start", Partial: partial})
	s.Push(&ai.AssistantMessageEventDone{Type: "done", Reason: msg.StopReason, Message: msg})
	s.End(msg)
	return s
}

func TestAgentLoop_BasicEventsAndCustomMessages(t *testing.T) {
	sp := "You are helpful."
	ctx := &agent.AgentContext{
		Messages: []agent.AgentMessage{ai.CreateInitialSystemMessage(&sp, nil)},
		Tools:    []*agent.AgentTool{},
	}
	prompt := talCreateUserMessage("Hello")

	var capturedIdentity *ai.ClientIdentity
	var capturedTranscript *ai.TranscriptContext

	cfg := &agent.AgentLoopConfig{
		Model:        talMockModel(),
		ConvertToLlm: talIdentityConvertToLlm,
	}

	streamFn := func(_ context.Context, _ *ai.Model, transcript *ai.TranscriptContext, opts *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
		capturedTranscript = transcript
		capturedIdentity = opts.Identity
		return talPushAssistantDone(talCreateAssistantMessage(
			ai.ContentList{&ai.TextContent{Type: "text", Text: "Hi there!"}},
			ai.StopReasonStop,
		)), nil
	}

	stream := agent.AgentLoop(t.Context(), []agent.AgentMessage{prompt}, ctx, cfg, streamFn)
	var events []agent.AgentEvent
	for ev := range stream.All() {
		events = append(events, ev)
	}
	msgs, err := stream.Result(t.Context())
	if err != nil {
		t.Fatalf("stream.Result: %v", err)
	}

	wantOrder := []string{
		"agent_start",
		"turn_start",
		"message_start:user",
		"message_end:user",
		"message_start:assistant",
		"message_end:assistant",
		"turn_end",
		"agent_end",
	}
	if got := talEventTypes(events); !reflect.DeepEqual(got, wantOrder) {
		t.Fatalf("event order mismatch:\ngot:  %v\nwant: %v", got, wantOrder)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 new messages, got %d", len(msgs))
	}
	if capturedTranscript == nil || ai.GetCurrentSystemPrompt(capturedTranscript.Messages) != "You are helpful." {
		t.Fatalf("expected system prompt normalized in transcript")
	}
	if capturedIdentity == nil || !capturedIdentity.Attribution.IsSome() {
		t.Fatalf("expected DefaultAgentIdentity merged into options")
	}
}

func TestAgentLoop_TransformContextAndConvertToLlm(t *testing.T) {
	artifact := &talCustomArtifactMessage{Role: "artifact", Note: "ui-only"}
	user := talCreateUserMessage("question")

	var llmMessagesSeen []ai.Message
	cfg := &agent.AgentLoopConfig{
		Model: talMockModel(),
		TransformContext: func(_ context.Context, msgs []agent.AgentMessage) ([]agent.AgentMessage, error) {
			return append(msgs, artifact), nil
		},
		ConvertToLlm: func(_ context.Context, msgs []agent.AgentMessage) ([]ai.Message, error) {
			return agent.DefaultConvertToLlm(msgs), nil
		},
	}

	streamFn := func(_ context.Context, _ *ai.Model, transcript *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
		llmMessagesSeen = transcript.Messages
		return talPushAssistantDone(talCreateAssistantMessage(
			ai.ContentList{&ai.TextContent{Type: "text", Text: "answer"}},
			ai.StopReasonStop,
		)), nil
	}

	newMsgs, err := agent.RunAgentLoop(t.Context(), []agent.AgentMessage{user, artifact}, &agent.AgentContext{}, cfg, nil, streamFn)
	if err != nil {
		t.Fatalf("RunAgentLoop: %v", err)
	}
	if len(newMsgs) != 3 {
		t.Fatalf("expected custom artifact preserved in newMessages (len 3), got %d", len(newMsgs))
	}
	for _, m := range llmMessagesSeen {
		if m.GetRole() == "artifact" {
			t.Fatalf("artifact message leaked into LLM transcript")
		}
	}
}

func TestAgentLoop_ParallelVsSequentialToolExecutionOrdering(t *testing.T) {
	t.Run("parallel emits start in source order, end in completion order, results in source order", func(t *testing.T) {
		releaseSlow := make(chan struct{})
		var releaseOnce sync.Once

		tool := &agent.AgentTool{
			Name:        "delay",
			Label:       "Delay",
			Description: "Delay tool",
			Parameters:  talSimpleSchema("id"),
			Execute: func(_ context.Context, _ string, params any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
				obj := params.(*jsonx.Object)
				idVal, _ := obj.Get("id")
				id := idVal.(string)
				if id == "slow" {
					onUpdate(agent.AgentToolResult{
						Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "working"}},
					})
					<-releaseSlow
				}
				return agent.AgentToolResult{
					Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "done:" + id}},
					Details: jsonx.ObjectOf("id", id),
				}, nil
			},
		}

		turn := 0
		streamFn := func(_ context.Context, _ *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
			turn++
			if turn == 1 {
				return talPushAssistantDone(talCreateAssistantMessage(ai.ContentList{
					&ai.ToolCall{Type: "toolCall", Id: "tc-slow", Name: "delay", Arguments: jsonx.ObjectOf("id", "slow")},
					&ai.ToolCall{Type: "toolCall", Id: "tc-fast", Name: "delay", Arguments: jsonx.ObjectOf("id", "fast")},
				}, ai.StopReasonToolUse)), nil
			}
			return talPushAssistantDone(talCreateAssistantMessage(
				ai.ContentList{&ai.TextContent{Type: "text", Text: "all done"}},
				ai.StopReasonStop,
			)), nil
		}

		var events []agent.AgentEvent
		cfg := &agent.AgentLoopConfig{
			Model:         talMockModel(),
			ToolExecution: agent.ToolExecutionModeParallel,
			ConvertToLlm:  talIdentityConvertToLlm,
		}
		_, err := agent.RunAgentLoop(
			t.Context(),
			[]agent.AgentMessage{talCreateUserMessage("go")},
			&agent.AgentContext{Tools: []*agent.AgentTool{tool}},
			cfg,
			func(ev agent.AgentEvent) {
				events = append(events, ev)
				if e, ok := ev.(*agent.AgentEventToolExecutionEnd); ok && e.ToolCallId == "tc-fast" {
					releaseOnce.Do(func() { close(releaseSlow) })
				}
			},
			streamFn,
		)
		if err != nil {
			t.Fatalf("RunAgentLoop: %v", err)
		}

		var toolPhase []string
		var toolResults []*ai.ToolResultMessage
		for _, ev := range events {
			switch e := ev.(type) {
			case *agent.AgentEventToolExecutionStart:
				toolPhase = append(toolPhase, "start:"+e.ToolCallId)
			case *agent.AgentEventToolExecutionEnd:
				toolPhase = append(toolPhase, "end:"+e.ToolCallId)
			case *agent.AgentEventMessageEnd:
				if tr, ok := e.Message.(*ai.ToolResultMessage); ok {
					toolPhase = append(toolPhase, "msg:"+tr.ToolCallId)
					toolResults = append(toolResults, tr)
				}
			}
		}

		wantPhase := []string{
			"start:tc-slow",
			"start:tc-fast",
			"end:tc-fast",
			"end:tc-slow",
			"msg:tc-slow",
			"msg:tc-fast",
		}
		if !reflect.DeepEqual(toolPhase, wantPhase) {
			t.Fatalf("parallel tool phase order mismatch:\ngot:  %v\nwant: %v", toolPhase, wantPhase)
		}
		if len(toolResults) != 2 || toolResults[0].ToolCallId != "tc-slow" || toolResults[1].ToolCallId != "tc-fast" {
			t.Fatalf("expected toolResults in source order, got %+v", toolResults)
		}
	})

	t.Run("sequential emits start/end/result per call in source order", func(t *testing.T) {
		var order []string
		tool := &agent.AgentTool{
			Name:        "seq",
			Label:       "Seq",
			Description: "Seq tool",
			Parameters:  talSimpleSchema("id"),
			Execute: func(_ context.Context, id string, _ any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
				order = append(order, id)
				return agent.AgentToolResult{
					Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "ok"}},
				}, nil
			},
		}

		turn := 0
		streamFn := func(_ context.Context, _ *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
			turn++
			if turn == 1 {
				return talPushAssistantDone(talCreateAssistantMessage(ai.ContentList{
					&ai.ToolCall{Type: "toolCall", Id: "t1", Name: "seq", Arguments: jsonx.ObjectOf("id", "1")},
					&ai.ToolCall{Type: "toolCall", Id: "t2", Name: "seq", Arguments: jsonx.ObjectOf("id", "2")},
				}, ai.StopReasonToolUse)), nil
			}
			return talPushAssistantDone(talCreateAssistantMessage(
				ai.ContentList{&ai.TextContent{Type: "text", Text: "done"}},
				ai.StopReasonStop,
			)), nil
		}

		var events []agent.AgentEvent
		cfg := &agent.AgentLoopConfig{
			Model:         talMockModel(),
			ToolExecution: agent.ToolExecutionModeSequential,
			ConvertToLlm:  talIdentityConvertToLlm,
		}
		_, err := agent.RunAgentLoop(
			t.Context(),
			[]agent.AgentMessage{talCreateUserMessage("go")},
			&agent.AgentContext{Tools: []*agent.AgentTool{tool}},
			cfg,
			func(ev agent.AgentEvent) { events = append(events, ev) },
			streamFn,
		)
		if err != nil {
			t.Fatalf("RunAgentLoop: %v", err)
		}

		var seqPhase []string
		for _, ev := range events {
			switch e := ev.(type) {
			case *agent.AgentEventToolExecutionStart:
				seqPhase = append(seqPhase, "start:"+e.ToolCallId)
			case *agent.AgentEventToolExecutionEnd:
				seqPhase = append(seqPhase, "end:"+e.ToolCallId)
			case *agent.AgentEventMessageEnd:
				if tr, ok := e.Message.(*ai.ToolResultMessage); ok {
					seqPhase = append(seqPhase, "msg:"+tr.ToolCallId)
				}
			}
		}
		want := []string{"start:t1", "end:t1", "msg:t1", "start:t2", "end:t2", "msg:t2"}
		if !reflect.DeepEqual(seqPhase, want) {
			t.Fatalf("sequential phase order mismatch:\ngot:  %v\nwant: %v", seqPhase, want)
		}
	})
}

// Tests commit 4438c58ba (fix(agent): answer every tool call when a batch is aborted).
func TestAgentLoop_AbortedBatchAnswersEveryToolCall(t *testing.T) {
	for _, mode := range []agent.ToolExecutionMode{agent.ToolExecutionModeSequential, agent.ToolExecutionModeParallel} {
		t.Run(string(mode), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			var executed []string
			var beforeCount int
			var mu sync.Mutex

			abortTool := &agent.AgentTool{
				Name:        "work",
				Label:       "Work",
				Description: "Work tool",
				Parameters:  talSimpleSchema("id"),
				Execute: func(_ context.Context, callID string, params any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
					mu.Lock()
					executed = append(executed, callID)
					mu.Unlock()
					if callID == "call-1" {
						cancel()
						return agent.AgentToolResult{
							Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "first completed and cancelled"}},
						}, nil
					}
					return agent.AgentToolResult{
						Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "should be skipped after cancel"}},
					}, nil
				},
			}

			turn := 0
			streamFn := func(c context.Context, _ *ai.Model, transcript *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
				turn++
				if turn == 1 {
					return talPushAssistantDone(talCreateAssistantMessage(ai.ContentList{
						&ai.ToolCall{Type: "toolCall", Id: "call-1", Name: "work", Arguments: jsonx.ObjectOf("id", "1")},
						&ai.ToolCall{Type: "toolCall", Id: "call-2", Name: "work", Arguments: jsonx.ObjectOf("id", "2")},
						&ai.ToolCall{Type: "toolCall", Id: "call-3", Name: "missing_tool", Arguments: jsonx.NewObject()},
					}, ai.StopReasonToolUse)), nil
				}
				return talPushAssistantDone(talCreateAssistantMessage(
					ai.ContentList{&ai.TextContent{Type: "text", Text: "aborted"}},
					ai.StopReasonAborted,
					"Operation aborted",
				)), nil
			}

			var events []agent.AgentEvent
			cfg := &agent.AgentLoopConfig{
				Model:         talMockModel(),
				ToolExecution: mode,
				ConvertToLlm:  talIdentityConvertToLlm,
				BeforeToolCall: func(_ context.Context, tc *agent.BeforeToolCallContext) (*agent.BeforeToolCallResult, error) {
					mu.Lock()
					beforeCount++
					mu.Unlock()
					return nil, nil
				},
			}

			// In parallel mode all three calls prepare BEFORE call-1 executes, so cancel inside call-1
			// only skips call-2 if queued sequentially; for pre-aborted test see dedicated subtest below.
			_, err := agent.RunAgentLoop(
				ctx,
				[]agent.AgentMessage{talCreateUserMessage("run")},
				&agent.AgentContext{Tools: []*agent.AgentTool{abortTool}},
				cfg,
				func(ev agent.AgentEvent) { events = append(events, ev) },
				streamFn,
			)
			if err != nil {
				t.Fatalf("RunAgentLoop: %v", err)
			}

			var turnEnd *agent.AgentEventTurnEnd
			for _, ev := range events {
				if te, ok := ev.(*agent.AgentEventTurnEnd); ok && len(te.ToolResults) > 0 {
					turnEnd = te
					break
				}
			}
			if turnEnd == nil {
				t.Fatalf("expected turn_end with 3 toolResults")
			}
			if len(turnEnd.ToolResults) != 3 {
				t.Fatalf("expected 3 toolResults (1 per tool call), got %d", len(turnEnd.ToolResults))
			}

			if mode == agent.ToolExecutionModeSequential {
				// Sequential mode: call-1 triggers cancel(), so call-2 & call-3 abort before BeforeToolCall or Execute.
				if !reflect.DeepEqual(executed, []string{"call-1"}) {
					t.Fatalf("sequential mode should only execute call-1, got %v", executed)
				}
				if beforeCount != 1 {
					t.Fatalf("expected BeforeToolCall invoked once (for call-1), got %d", beforeCount)
				}
				for _, tr := range turnEnd.ToolResults[1:] {
					if !tr.IsError {
						t.Fatalf("expected synthetic error ToolResult for %s", tr.ToolCallId)
					}
					if tr.DurationMs != nil {
						t.Fatalf("expected aborted skipped call %s to have nil DurationMs, got %d", tr.ToolCallId, *tr.DurationMs)
					}
					txt := tr.Content[0].(*ai.TextContent).Text
					if txt != "Operation aborted" {
						t.Fatalf("expected Operation aborted, got %q", txt)
					}
				}
			}
		})
	}

	t.Run("pre-aborted parallel batch still answers every tool call before beforeToolCall", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		beforeCalled := false
		toolCalled := false
		tool := &agent.AgentTool{
			Name:        "calc",
			Label:       "Calc",
			Description: "Calc",
			Parameters:  talSimpleSchema("x"),
			Execute: func(_ context.Context, _ string, _ any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
				toolCalled = true
				return agent.AgentToolResult{}, nil
			},
		}

		out := agent.RunToolCall(
			ctx,
			&agent.AgentToolCall{Type: "toolCall", Id: "c1", Name: "unknown", Arguments: jsonx.NewObject()},
			&agent.RunToolCallOptions{
				ToolCallHooks: agent.ToolCallHooks{
					BeforeToolCall: func(_ context.Context, _ *agent.BeforeToolCallContext) (*agent.BeforeToolCallResult, error) {
						beforeCalled = true
						return nil, nil
					},
				},
				Tools:   []*agent.AgentTool{tool},
				Context: &agent.AgentContext{},
			},
		)
		if beforeCalled || toolCalled {
			t.Fatalf("pre-aborted RunToolCall should not invoke beforeToolCall or Execute")
		}
		if !out.IsError || out.DurationMs != nil {
			t.Fatalf("expected IsError=true and DurationMs=nil, got %+v", out)
		}
		if out.Result.Content[0].(*ai.TextContent).Text != "Operation aborted" {
			t.Fatalf("expected Operation aborted (even for unknown tool), got %q", out.Result.Content[0].(*ai.TextContent).Text)
		}
	})
}

func TestAgentLoop_TruncatedToolCallsFailWithoutExecuting(t *testing.T) {
	executed := false
	beforeCalled := false
	tool := &agent.AgentTool{
		Name:        "calc",
		Label:       "Calc",
		Description: "Calc",
		Parameters:  talSimpleSchema("expr"),
		Execute: func(_ context.Context, _ string, _ any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
			executed = true
			return agent.AgentToolResult{}, nil
		},
	}

	turn := 0
	streamFn := func(_ context.Context, _ *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
		turn++
		if turn == 1 {
			return talPushAssistantDone(talCreateAssistantMessage(ai.ContentList{
				&ai.ToolCall{Type: "toolCall", Id: "tc-trunc", Name: "calc", Arguments: jsonx.NewObject()},
			}, ai.StopReasonLength)), nil
		}
		return talPushAssistantDone(talCreateAssistantMessage(
			ai.ContentList{&ai.TextContent{Type: "text", Text: "reissued"}},
			ai.StopReasonStop,
		)), nil
	}

	cfg := &agent.AgentLoopConfig{
		Model:        talMockModel(),
		ConvertToLlm: talIdentityConvertToLlm,
		BeforeToolCall: func(_ context.Context, _ *agent.BeforeToolCallContext) (*agent.BeforeToolCallResult, error) {
			beforeCalled = true
			return nil, nil
		},
	}

	msgs, err := agent.RunAgentLoop(
		t.Context(),
		[]agent.AgentMessage{talCreateUserMessage("go")},
		&agent.AgentContext{Tools: []*agent.AgentTool{tool}},
		cfg,
		nil,
		streamFn,
	)
	if err != nil {
		t.Fatalf("RunAgentLoop: %v", err)
	}
	if executed || beforeCalled {
		t.Fatalf("truncated tool calls must not invoke BeforeToolCall or Execute")
	}
	var tr *ai.ToolResultMessage
	for _, m := range msgs {
		if candidate, ok := m.(*ai.ToolResultMessage); ok {
			tr = candidate
			break
		}
	}
	if tr == nil || !tr.IsError {
		t.Fatalf("expected truncated ToolResultMessage error in %#v", msgs)
	}
	txt := tr.Content[0].(*ai.TextContent).Text
	if !strings.Contains(txt, "output token limit") {
		t.Fatalf("expected output token limit explanation, got %q", txt)
	}
	if msgs[0].GetRole() != "system" || msgs[len(msgs)-1].GetRole() != "assistant" {
		t.Fatalf("expected [system ... assistant], got %d msgs", len(msgs))
	}
}

func TestAgentLoop_HooksPrepareArgumentsAndLateUpdateGuard(t *testing.T) {
	t.Run("prepareArguments runs before validation and preserves original args", func(t *testing.T) {
		var seenExecVal string
		tool := &agent.AgentTool{
			Name:        "trim",
			Label:       "Trim",
			Description: "Trim",
			Parameters:  talSimpleSchema("value"),
			PrepareArguments: func(args any) any {
				obj := args.(*jsonx.Object)
				v, _ := obj.Get("value")
				return jsonx.ObjectOf("value", strings.TrimSpace(v.(string)))
			},
			Execute: func(_ context.Context, _ string, params any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
				v, _ := params.(*jsonx.Object).Get("value")
				seenExecVal = v.(string)
				return agent.AgentToolResult{
					Content:           ai.ContentList{&ai.TextContent{Type: "text", Text: seenExecVal}},
					StructuredContent: jsonx.ObjectOf("raw", true),
				}, nil
			},
		}

		out := agent.RunToolCall(
			t.Context(),
			&agent.AgentToolCall{Type: "toolCall", Id: "tc1", Name: "trim", Arguments: jsonx.ObjectOf("value", "  hi  ")},
			&agent.RunToolCallOptions{
				Tools:   []*agent.AgentTool{tool},
				Context: &agent.AgentContext{},
				ToolCallHooks: agent.ToolCallHooks{
					AfterToolCall: func(_ context.Context, ctx *agent.AfterToolCallContext) (*agent.AfterToolCallResult, error) {
						// Replacing Content without StructuredContent clears structuredContent.
						return &agent.AfterToolCallResult{
							Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "overridden"}},
						}, nil
					},
				},
			},
		)

		if seenExecVal != "hi" {
			t.Fatalf("expected trimmed prepared arg 'hi', got %q", seenExecVal)
		}
		if out.Result.Content[0].(*ai.TextContent).Text != "overridden" {
			t.Fatalf("expected afterToolCall override, got %q", out.Result.Content[0].(*ai.TextContent).Text)
		}
		if out.Result.StructuredContent != nil {
			t.Fatalf("expected StructuredContent cleared when only Content replaced, got %+v", out.Result.StructuredContent)
		}
		if out.DurationMs == nil {
			t.Fatalf("expected executed tool call to have DurationMs")
		}
	})

	t.Run("beforeToolCall block and late onUpdate suppression", func(t *testing.T) {
		blocked := agent.RunToolCall(
			t.Context(),
			&agent.AgentToolCall{Type: "toolCall", Id: "tc-block", Name: "calculate", Arguments: jsonx.ObjectOf("expression", "1+1")},
			&agent.RunToolCallOptions{
				Tools:   []*agent.AgentTool{testkit.CalculateTool()},
				Context: &agent.AgentContext{},
				ToolCallHooks: agent.ToolCallHooks{
					BeforeToolCall: func(_ context.Context, _ *agent.BeforeToolCallContext) (*agent.BeforeToolCallResult, error) {
						return &agent.BeforeToolCallResult{Block: true, Reason: "policy denied"}, nil
					},
				},
			},
		)
		if !blocked.IsError || blocked.Result.Content[0].(*ai.TextContent).Text != "policy denied" {
			t.Fatalf("unexpected blocked tool result: %+v", blocked)
		}
		if blocked.DurationMs != nil {
			t.Fatalf("blocked call must not have DurationMs")
		}

		// Verify late onUpdate callback fired after Execute returns is suppressed.
		var lateFire func()
		doneCh := make(chan struct{})
		tool := &agent.AgentTool{
			Name:        "late",
			Label:       "Late",
			Description: "Late",
			Parameters:  talSimpleSchema(),
			Execute: func(_ context.Context, _ string, _ any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
				onUpdate(agent.AgentToolResult{Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "early"}}})
				lateFire = func() {
					onUpdate(agent.AgentToolResult{Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "late"}}})
					close(doneCh)
				}
				return agent.AgentToolResult{Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "final"}}}, nil
			},
		}

		var updates []agent.AgentToolResult
		res := agent.RunToolCall(
			t.Context(),
			&agent.AgentToolCall{Type: "toolCall", Id: "tc-late", Name: "late", Arguments: jsonx.NewObject()},
			&agent.RunToolCallOptions{
				Tools:    []*agent.AgentTool{tool},
				Context:  &agent.AgentContext{},
				OnUpdate: func(partial agent.AgentToolResult) { updates = append(updates, partial) },
			},
		)
		if res.IsError {
			t.Fatalf("unexpected error: %+v", res)
		}
		lateFire()
		<-doneCh
		if len(updates) != 1 || updates[0].Content[0].(*ai.TextContent).Text != "early" {
			t.Fatalf("expected only 1 update ('early') before Execute finished (late ignored), got %+v", updates)
		}
	})
}

func TestAgentLoop_FinishTurnPrepareRequestPrepareNextTurnAndTerminate(t *testing.T) {
	t.Run("ToolResult Terminate ends turn cleanly without follow-up LLM call", func(t *testing.T) {
		term := true
		termTool := &agent.AgentTool{
			Name:        "finish",
			Label:       "Finish",
			Description: "Finish",
			Parameters:  talSimpleSchema(),
			Execute: func(_ context.Context, _ string, _ any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
				return agent.AgentToolResult{
					Content:   ai.ContentList{&ai.TextContent{Type: "text", Text: "stopped"}},
					Terminate: &term,
				}, nil
			},
		}

		llmCalls := 0
		streamFn := func(_ context.Context, _ *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
			llmCalls++
			return talPushAssistantDone(talCreateAssistantMessage(ai.ContentList{
				&ai.ToolCall{Type: "toolCall", Id: "tc-term", Name: "finish", Arguments: jsonx.NewObject()},
			}, ai.StopReasonToolUse)), nil
		}

		msgs, err := agent.RunAgentLoop(
			t.Context(),
			[]agent.AgentMessage{talCreateUserMessage("stop")},
			&agent.AgentContext{Tools: []*agent.AgentTool{termTool}},
			&agent.AgentLoopConfig{Model: talMockModel(), ConvertToLlm: talIdentityConvertToLlm},
			nil,
			streamFn,
		)
		if err != nil {
			t.Fatalf("RunAgentLoop: %v", err)
		}
		if llmCalls != 1 {
			t.Fatalf("expected Terminate tool to prevent turn 2 LLM call, got %d calls", llmCalls)
		}
		if len(msgs) != 4 {
			t.Fatalf("expected [system, user, assistant, toolResult], got %d msgs", len(msgs))
		}
	})

	t.Run("FinishTurn end skips queues and prepareNextTurn", func(t *testing.T) {
		turn := 0
		prepareNextCalls := 0
		followUpPolls := 0
		streamFn := func(_ context.Context, _ *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
			turn++
			return talPushAssistantDone(talCreateAssistantMessage(ai.ContentList{
				&ai.ToolCall{Type: "toolCall", Id: "tc1", Name: "calculate", Arguments: jsonx.ObjectOf("expression", "1+1")},
			}, ai.StopReasonToolUse)), nil
		}

		cfg := &agent.AgentLoopConfig{
			Model:        talMockModel(),
			ConvertToLlm: talIdentityConvertToLlm,
			FinishTurn: func(_ context.Context, tc *agent.AgentTurnContext) (*agent.AgentTurnDecision, error) {
				if tc.Message == nil || len(tc.ToolResults) != 1 || tc.Context == nil || len(tc.Context.Messages) != 4 {
					t.Fatalf("unexpected FinishTurn context: %+v", tc)
				}
				return &agent.AgentTurnDecision{Action: agent.AgentTurnDecisionEnd}, nil
			},
			PrepareNextTurn: func(_ context.Context, _ *agent.PrepareNextTurnContext) (*agent.AgentLoopTurnUpdate, error) {
				prepareNextCalls++
				return nil, nil
			},
			GetFollowUpMessages: func(context.Context) ([]agent.AgentMessage, error) {
				followUpPolls++
				return []agent.AgentMessage{talCreateUserMessage("queued")}, nil
			},
		}

		msgs, err := agent.RunAgentLoop(
			t.Context(),
			[]agent.AgentMessage{talCreateUserMessage("go")},
			&agent.AgentContext{Tools: []*agent.AgentTool{testkit.CalculateTool()}},
			cfg,
			nil,
			streamFn,
		)
		if err != nil {
			t.Fatalf("RunAgentLoop: %v", err)
		}
		if turn != 1 || len(msgs) != 4 {
			t.Fatalf("expected end after turn 1 with 4 messages, got turn=%d len=%d", turn, len(msgs))
		}
		if prepareNextCalls != 0 || followUpPolls != 0 {
			t.Fatalf("expected action:end to skip prepareNextTurn (%d) and followUp (%d)", prepareNextCalls, followUpPolls)
		}
	})

	t.Run("PrepareRequest and PrepareNextTurn update model, tools, and thinkingLevel dynamically", func(t *testing.T) {
		altModel := talMockModel()
		altModel.Id = "alt-model"
		baseModel := talMockModel()
		toolA := &agent.AgentTool{
			Name:        "toolA",
			Label:       "A",
			Description: "A",
			Parameters:  talSimpleSchema(),
			Execute: func(_ context.Context, _ string, _ any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
				return agent.AgentToolResult{Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "a"}}}, nil
			},
		}
		toolB := &agent.AgentTool{
			Name:        "toolB",
			Label:       "B",
			Description: "B",
			Parameters:  talSimpleSchema(),
			Execute: func(_ context.Context, _ string, _ any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
				return agent.AgentToolResult{Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "b"}}}, nil
			},
		}

		var seenModels []string
		var seenToolNames [][]string
		var seenThinking []*ai.ThinkingLevel
		call := 0
		streamFn := func(_ context.Context, m *ai.Model, transcript *ai.TranscriptContext, opts *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
			call++
			seenModels = append(seenModels, m.Id)
			seenThinking = append(seenThinking, opts.Reasoning)
			var names []string
			for _, dt := range ai.GetCurrentTools(transcript.Messages) {
				names = append(names, dt.Name)
			}
			seenToolNames = append(seenToolNames, names)
			if call == 2 {
				return talPushAssistantDone(talCreateAssistantMessage(
					ai.ContentList{&ai.TextContent{Type: "text", Text: "done"}},
					ai.StopReasonStop,
				)), nil
			}
			return talPushAssistantDone(talCreateAssistantMessage(ai.ContentList{
				&ai.ToolCall{Type: "toolCall", Id: "tcA", Name: "toolA", Arguments: jsonx.NewObject()},
			}, ai.StopReasonToolUse)), nil
		}

		tlHigh := agent.ThinkingLevelHigh
		spInit := "initial"
		spUpdated := "Updated system prompt"
		cfg := &agent.AgentLoopConfig{
			Model:        baseModel,
			ConvertToLlm: talIdentityConvertToLlm,
			PrepareRequest: func(_ context.Context, req *agent.PrepareRequestContext) (*agent.AgentRequestUpdate, error) {
				if req.Model.Id == "mock-model" && len(seenModels) == 0 {
					return &agent.AgentRequestUpdate{Model: altModel}, nil
				}
				return nil, nil
			},
			PrepareNextTurn: func(_ context.Context, next *agent.PrepareNextTurnContext) (*agent.AgentLoopTurnUpdate, error) {
				if next.Message == nil || len(next.ToolResults) != 1 {
					t.Fatalf("expected assistant message and 1 toolResult in PrepareNextTurnContext")
				}
				return &agent.AgentLoopTurnUpdate{
					Context: &agent.AgentContext{
						Messages: next.Context.Messages,
						Tools:    []*agent.AgentTool{toolB},
					},
					Messages:      agent.AgentMessageList{ai.CreateInitialSystemMessage(&spUpdated, nil)},
					Model:         baseModel,
					ThinkingLevel: &tlHigh,
				}, nil
			},
		}

		_, err := agent.RunAgentLoop(
			t.Context(),
			[]agent.AgentMessage{talCreateUserMessage("go")},
			&agent.AgentContext{
				Messages: []agent.AgentMessage{ai.CreateInitialSystemMessage(&spInit, nil)},
				Tools:    []*agent.AgentTool{toolA},
			},
			cfg,
			nil,
			streamFn,
		)
		if err != nil {
			t.Fatalf("RunAgentLoop: %v", err)
		}
		if !reflect.DeepEqual(seenModels, []string{"alt-model", "mock-model"}) {
			t.Fatalf("PrepareRequest model override check failed: %v", seenModels)
		}
		if !reflect.DeepEqual(seenToolNames, [][]string{{"toolA"}, {"toolB"}}) {
			t.Fatalf("PrepareNextTurn tools update check failed: %v", seenToolNames)
		}
		if len(seenThinking) != 2 || seenThinking[1] == nil || *seenThinking[1] != ai.ThinkingLevelHigh {
			t.Fatalf("expected updated ThinkingLevel high on turn 2, got %v", seenThinking)
		}
	})
}

func TestAgentLoop_ContinueAndQueuesAndDefaultStreamFn(t *testing.T) {
	t.Run("AgentLoopContinue errors cleanly on invalid trailing state", func(t *testing.T) {
		cfg := &agent.AgentLoopConfig{Model: talMockModel(), ConvertToLlm: talIdentityConvertToLlm}
		if _, err := agent.AgentLoopContinue(t.Context(), &agent.AgentContext{Messages: []agent.AgentMessage{}}, cfg, nil); err == nil || !strings.Contains(err.Error(), "no messages") {
			t.Fatalf("expected no messages error, got %v", err)
		}
		if _, err := agent.AgentLoopContinue(t.Context(), &agent.AgentContext{
			Messages: []agent.AgentMessage{talCreateAssistantMessage(ai.ContentList{}, ai.StopReasonStop)},
		}, cfg, nil); err == nil || !strings.Contains(err.Error(), "assistant") {
			t.Fatalf("expected assistant role error, got %v", err)
		}
	})

	t.Run("AgentLoopContinue resumes from trailing user message and runs followUps", func(t *testing.T) {
		turn := 0
		followUpSent := false
		streamFn := func(_ context.Context, _ *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
			turn++
			return talPushAssistantDone(talCreateAssistantMessage(
				ai.ContentList{&ai.TextContent{Type: "text", Text: "resp"}},
				ai.StopReasonStop,
			)), nil
		}

		cfg := &agent.AgentLoopConfig{
			Model:        talMockModel(),
			ConvertToLlm: talIdentityConvertToLlm,
			GetFollowUpMessages: func(_ context.Context) ([]agent.AgentMessage, error) {
				if !followUpSent {
					followUpSent = true
					return []agent.AgentMessage{talCreateUserMessage("follow up")}, nil
				}
				return nil, nil
			},
		}

		stream, err := agent.AgentLoopContinue(t.Context(), &agent.AgentContext{
			Messages: []agent.AgentMessage{talCreateUserMessage("initial")},
		}, cfg, streamFn)
		if err != nil {
			t.Fatalf("AgentLoopContinue: %v", err)
		}
		newMsgs, err := stream.Result(t.Context())
		if err != nil {
			t.Fatalf("Result: %v", err)
		}
		if turn != 2 || len(newMsgs) != 3 {
			t.Fatalf("expected 2 turns and 3 new messages, got turn=%d msgs=%d", turn, len(newMsgs))
		}
	})

	t.Run("SetDefaultStreamFn updates and restores", func(t *testing.T) {
		orig := agent.GetDefaultStreamFn()
		defer agent.SetDefaultStreamFn(orig)

		called := false
		custom := func(_ context.Context, _ *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
			called = true
			return talPushAssistantDone(talCreateAssistantMessage(ai.ContentList{&ai.TextContent{Type: "text", Text: "ok"}}, ai.StopReasonStop)), nil
		}
		agent.SetDefaultStreamFn(custom)

		gotPtr := reflect.ValueOf(agent.GetDefaultStreamFn()).Pointer()
		wantPtr := reflect.ValueOf(custom).Pointer()
		if gotPtr != wantPtr {
			t.Fatalf("expected GetDefaultStreamFn to return custom fn")
		}
		_, err := agent.RunAgentLoop(
			t.Context(),
			[]agent.AgentMessage{talCreateUserMessage("hi")},
			&agent.AgentContext{},
			&agent.AgentLoopConfig{Model: talMockModel(), ConvertToLlm: talIdentityConvertToLlm},
			nil,
			nil,
		)
		if err != nil || !called {
			t.Fatalf("expected RunAgentLoop with nil streamFn to call default streamFn, err=%v called=%v", err, called)
		}

		agent.SetDefaultStreamFn(orig)
		if reflect.ValueOf(agent.GetDefaultStreamFn()).Pointer() != reflect.ValueOf(orig).Pointer() {
			t.Fatalf("expected default streamFn restored")
		}
	})

	t.Run("ToolCall validation errors come back as ToolResult IsError without crashing", func(t *testing.T) {
		out := agent.RunToolCall(
			t.Context(),
			&agent.AgentToolCall{Type: "toolCall", Id: "tc-bad", Name: "calculate", Arguments: jsonx.NewObject()},
			&agent.RunToolCallOptions{
				Tools:   []*agent.AgentTool{testkit.CalculateTool()},
				Context: &agent.AgentContext{},
			},
		)
		if !out.IsError {
			t.Fatalf("expected validation error ToolCallOutcome")
		}
		if out.DurationMs != nil {
			t.Fatalf("validation failure must not set DurationMs")
		}
	})
}

func TestAgentLoop_AgentEventJSONRoundTripAndUnknownMessage(t *testing.T) {
	custom := &talCustomArtifactMessage{Role: "artifact", Note: "hello"}
	raw, err := jsonx.Marshal(agent.AgentEventAgentEnd{
		Type:     "agent_end",
		Messages: agent.AgentMessageList{talCreateUserMessage("hi"), custom},
	})
	if err != nil {
		t.Fatalf("marshal agent_end: %v", err)
	}
	ev, err := agent.UnmarshalAgentEvent(raw)
	if err != nil {
		t.Fatalf("UnmarshalAgentEvent: %v", err)
	}
	end, ok := ev.(*agent.AgentEventAgentEnd)
	if !ok {
		t.Fatalf("expected *AgentEventAgentEnd, got %T", ev)
	}
	if len(end.Messages) != 2 {
		t.Fatalf("expected 2 decoded messages, got %d", len(end.Messages))
	}
	if _, ok := end.Messages[0].(*ai.UserMessage); !ok {
		t.Fatalf("expected UserMessage, got %T", end.Messages[0])
	}
	unk, ok := end.Messages[1].(*agent.UnknownAgentMessage)
	if !ok || unk.GetRole() != "artifact" {
		t.Fatalf("expected UnknownAgentMessage with role 'artifact', got %#v", end.Messages[1])
	}
	reMarshaled, err := jsonx.Marshal(unk)
	if err != nil || !strings.Contains(string(reMarshaled), `"note":"hello"`) {
		t.Fatalf("expected UnknownAgentMessage to preserve raw JSON, got %s (err=%v)", reMarshaled, err)
	}
}

func talPushAssistantError(msg *ai.AssistantMessage, reason ai.StopReason) *ai.AssistantMessageEventStream {
	s := ai.NewAssistantMessageEventStream()
	partial := &ai.AssistantMessage{
		Role:       "assistant",
		Content:    ai.ContentList{},
		Api:        msg.Api,
		Provider:   msg.Provider,
		Model:      msg.Model,
		StopReason: reason,
		Timestamp:  msg.Timestamp,
	}
	s.Push(&ai.AssistantMessageEventStart{Type: "start", Partial: partial})
	s.Push(&ai.AssistantMessageEventError{Type: "error", Reason: reason, Error: msg})
	s.End(msg)
	return s
}

func TestAgentLoop_V152ToolExecutionAndHooksParity(t *testing.T) {
	t.Run("forces sequential batch when any called tool has ExecutionModeSequential", func(t *testing.T) {
		var order []string
		var mu sync.Mutex
		releaseSlow := make(chan struct{})

		slow := &agent.AgentTool{
			Name:          "slow",
			Label:         "Slow",
			Description:   "Slow",
			Parameters:    talSimpleSchema("value"),
			ExecutionMode: agent.ToolExecutionModeSequential,
			Execute: func(_ context.Context, _ string, params any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
				v, _ := params.(*jsonx.Object).Get("value")
				mu.Lock()
				order = append(order, "start:slow:"+v.(string))
				mu.Unlock()
				if v.(string) == "a" {
					<-releaseSlow
				}
				mu.Lock()
				order = append(order, "end:slow:"+v.(string))
				mu.Unlock()
				return agent.AgentToolResult{Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "slow"}}}, nil
			},
		}
		fast := &agent.AgentTool{
			Name:        "fast",
			Label:       "Fast",
			Description: "Fast",
			Parameters:  talSimpleSchema("value"),
			Execute: func(_ context.Context, _ string, params any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
				v, _ := params.(*jsonx.Object).Get("value")
				mu.Lock()
				order = append(order, "start:fast:"+v.(string))
				mu.Unlock()
				return agent.AgentToolResult{Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "fast"}}}, nil
			},
		}

		call := 0
		streamFn := func(_ context.Context, _ *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
			call++
			if call == 1 {
				s := talPushAssistantDone(talCreateAssistantMessage(ai.ContentList{
					&ai.ToolCall{Type: "toolCall", Id: "tc-1", Name: "slow", Arguments: jsonx.ObjectOf("value", "a")},
					&ai.ToolCall{Type: "toolCall", Id: "tc-2", Name: "fast", Arguments: jsonx.ObjectOf("value", "b")},
				}, ai.StopReasonToolUse))
				go func() {
					time.Sleep(15 * time.Millisecond)
					close(releaseSlow)
				}()
				return s, nil
			}
			return talPushAssistantDone(talCreateAssistantMessage(ai.ContentList{&ai.TextContent{Type: "text", Text: "done"}}, ai.StopReasonStop)), nil
		}

		_, err := agent.RunAgentLoop(
			t.Context(),
			[]agent.AgentMessage{talCreateUserMessage("go")},
			&agent.AgentContext{Tools: []*agent.AgentTool{slow, fast}},
			&agent.AgentLoopConfig{
				Model:         talMockModel(),
				ToolExecution: agent.ToolExecutionModeParallel,
				ConvertToLlm:  talIdentityConvertToLlm,
			},
			nil,
			streamFn,
		)
		if err != nil {
			t.Fatalf("RunAgentLoop: %v", err)
		}
		want := []string{"start:slow:a", "end:slow:a", "start:fast:b"}
		if !reflect.DeepEqual(order, want) {
			t.Fatalf("expected sequential order %v, got %v", want, order)
		}
	})

	t.Run("allows parallel execution when all tools use ExecutionModeParallel", func(t *testing.T) {
		releaseFirst := make(chan struct{})
		var firstFinished atomic.Bool
		var parallelObserved atomic.Bool

		tool := &agent.AgentTool{
			Name:          "echo",
			Label:         "Echo",
			Description:   "Echo",
			Parameters:    talSimpleSchema("value"),
			ExecutionMode: agent.ToolExecutionModeParallel,
			Execute: func(_ context.Context, _ string, params any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
				v, _ := params.(*jsonx.Object).Get("value")
				if v.(string) == "first" {
					<-releaseFirst
					firstFinished.Store(true)
				} else if !firstFinished.Load() {
					parallelObserved.Store(true)
					close(releaseFirst)
				}
				return agent.AgentToolResult{Content: ai.ContentList{&ai.TextContent{Type: "text", Text: v.(string)}}}, nil
			},
		}

		call := 0
		streamFn := func(_ context.Context, _ *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
			call++
			if call == 1 {
				return talPushAssistantDone(talCreateAssistantMessage(ai.ContentList{
					&ai.ToolCall{Type: "toolCall", Id: "tc-1", Name: "echo", Arguments: jsonx.ObjectOf("value", "first")},
					&ai.ToolCall{Type: "toolCall", Id: "tc-2", Name: "echo", Arguments: jsonx.ObjectOf("value", "second")},
				}, ai.StopReasonToolUse)), nil
			}
			return talPushAssistantDone(talCreateAssistantMessage(ai.ContentList{&ai.TextContent{Type: "text", Text: "done"}}, ai.StopReasonStop)), nil
		}

		_, err := agent.RunAgentLoop(
			t.Context(),
			[]agent.AgentMessage{talCreateUserMessage("go")},
			&agent.AgentContext{Tools: []*agent.AgentTool{tool}},
			&agent.AgentLoopConfig{Model: talMockModel(), ConvertToLlm: talIdentityConvertToLlm},
			nil,
			streamFn,
		)
		if err != nil {
			t.Fatalf("RunAgentLoop: %v", err)
		}
		if !parallelObserved.Load() {
			t.Fatalf("expected parallel tools to overlap execution")
		}
	})

	t.Run("RunToolCall hooks, validation, DurationMs excluding hooks, and StructuredContent drop rule", func(t *testing.T) {
		echo := &agent.AgentTool{
			Name:         "echo",
			Label:        "Echo",
			Description:  "Echo",
			Parameters:   talSimpleSchema("value"),
			OutputSchema: talSimpleSchema("value"),
			Execute: func(_ context.Context, _ string, params any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
				onUpdate(agent.AgentToolResult{Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "partial"}}})
				v, _ := params.(*jsonx.Object).Get("value")
				return agent.AgentToolResult{
					Content:           ai.ContentList{&ai.TextContent{Type: "text", Text: v.(string)}},
					Details:           jsonx.ObjectOf("detail", true),
					StructuredContent: jsonx.ObjectOf("value", v.(string)),
				}, nil
			},
		}
		failing := &agent.AgentTool{
			Name:        "failing",
			Label:       "Failing",
			Description: "Failing",
			Parameters:  talSimpleSchema(),
			Execute: func(_ context.Context, _ string, _ any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
				errFlag := true
				return agent.AgentToolResult{
					Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "bad"}},
					Details: jsonx.ObjectOf("partial", true),
					IsError: &errFlag,
				}, nil
			},
		}

		var hookCalls []string
		var updates []agent.AgentToolResult
		assistantMsg := talCreateAssistantMessage(ai.ContentList{}, ai.StopReasonStop)
		opts := &agent.RunToolCallOptions{
			Tools:            []*agent.AgentTool{echo, failing},
			AssistantMessage: assistantMsg,
			Context:          &agent.AgentContext{},
			ToolCallHooks: agent.ToolCallHooks{
				BeforeToolCall: func(_ context.Context, tc *agent.BeforeToolCallContext) (*agent.BeforeToolCallResult, error) {
					hookCalls = append(hookCalls, "before:"+tc.ToolCall.Id)
					if obj, ok := tc.Args.(*jsonx.Object); ok {
						if v, _ := obj.Get("value"); v == "blocked" {
							return &agent.BeforeToolCallResult{Block: true, Reason: "nope"}, nil
						}
					}
					return nil, nil
				},
				AfterToolCall: func(_ context.Context, tc *agent.AfterToolCallContext) (*agent.AfterToolCallResult, error) {
					hookCalls = append(hookCalls, "after:"+tc.ToolCall.Id)
					return nil, nil
				},
			},
			OnUpdate: func(p agent.AgentToolResult) { updates = append(updates, p) },
		}

		outA := agent.RunToolCall(t.Context(), &agent.AgentToolCall{Type: "toolCall", Id: "a", Name: "echo", Arguments: jsonx.ObjectOf("value", "a")}, opts)
		if outA.IsError || outA.ToolCall.Id != "a" || outA.Result.StructuredContent == nil || outA.DurationMs == nil {
			t.Fatalf("unexpected outcome A: %+v", outA)
		}
		outB := agent.RunToolCall(t.Context(), &agent.AgentToolCall{Type: "toolCall", Id: "b", Name: "echo", Arguments: jsonx.ObjectOf("value", jsonx.ObjectOf("nested", true))}, opts)
		if !outB.IsError || outB.DurationMs != nil {
			t.Fatalf("expected validation error without DurationMs for B, got %+v", outB)
		}
		outC := agent.RunToolCall(t.Context(), &agent.AgentToolCall{Type: "toolCall", Id: "c", Name: "echo", Arguments: jsonx.ObjectOf("value", "blocked")}, opts)
		if !outC.IsError || outC.DurationMs != nil || outC.Result.Content[0].(*ai.TextContent).Text != "nope" {
			t.Fatalf("unexpected blocked outcome C: %+v", outC)
		}
		outD := agent.RunToolCall(t.Context(), &agent.AgentToolCall{Type: "toolCall", Id: "d", Name: "missing", Arguments: jsonx.NewObject()}, opts)
		if !outD.IsError || outD.DurationMs != nil || !strings.Contains(outD.Result.Content[0].(*ai.TextContent).Text, "Tool missing not found") {
			t.Fatalf("unexpected missing outcome D: %+v", outD)
		}
		outE := agent.RunToolCall(t.Context(), &agent.AgentToolCall{Type: "toolCall", Id: "e", Name: "failing", Arguments: jsonx.NewObject()}, opts)
		if !outE.IsError || outE.DurationMs == nil || outE.Result.Details == nil {
			t.Fatalf("expected error result with details and DurationMs for E, got %+v", outE)
		}
		if len(updates) != 1 {
			t.Fatalf("expected 1 update, got %d", len(updates))
		}
		wantHooks := []string{"before:a", "after:a", "before:c", "before:e", "after:e"}
		if !reflect.DeepEqual(hookCalls, wantHooks) {
			t.Fatalf("hookCalls mismatch: got %v want %v", hookCalls, wantHooks)
		}

		// Verify DurationMs excludes hook sleeps.
		outTimed := agent.RunToolCall(t.Context(), &agent.AgentToolCall{Type: "toolCall", Id: "t", Name: "echo", Arguments: jsonx.ObjectOf("value", "ok")}, &agent.RunToolCallOptions{
			Tools:   []*agent.AgentTool{echo},
			Context: &agent.AgentContext{},
			ToolCallHooks: agent.ToolCallHooks{
				BeforeToolCall: func(_ context.Context, _ *agent.BeforeToolCallContext) (*agent.BeforeToolCallResult, error) {
					time.Sleep(25 * time.Millisecond)
					return nil, nil
				},
				AfterToolCall: func(_ context.Context, _ *agent.AfterToolCallContext) (*agent.AfterToolCallResult, error) {
					time.Sleep(25 * time.Millisecond)
					return nil, nil
				},
			},
		})
		if outTimed.DurationMs == nil || *outTimed.DurationMs >= 40 {
			t.Fatalf("expected DurationMs to exclude 50ms of hooks, got %v", outTimed.DurationMs)
		}

		// Verify AfterToolCall StructuredContent replacement & drop rules + Usage merge.
		redacted := ai.ContentList{&ai.TextContent{Type: "text", Text: "redacted"}}
		cases := []struct {
			name      string
			after     *agent.AfterToolCallResult
			wantValue string
			wantNil   bool
		}{
			{name: "content only drops structuredContent", after: &agent.AfterToolCallResult{Content: redacted}, wantNil: true},
			{name: "structuredContent only replaces", after: &agent.AfterToolCallResult{StructuredContent: jsonx.Some[any](jsonx.ObjectOf("value", "replaced"))}, wantValue: "replaced"},
			{name: "both replaces", after: &agent.AfterToolCallResult{Content: redacted, StructuredContent: jsonx.Some[any](jsonx.ObjectOf("value", "both"))}, wantValue: "both"},
			{name: "details only preserves", after: &agent.AfterToolCallResult{Details: jsonx.Some[any](jsonx.ObjectOf("note", "kept"))}, wantValue: "original"},
		}
		for _, tc := range cases {
			after := tc.after
			out := agent.RunToolCall(t.Context(), &agent.AgentToolCall{Type: "toolCall", Id: "x", Name: "echo", Arguments: jsonx.ObjectOf("value", "original")}, &agent.RunToolCallOptions{
				Tools:   []*agent.AgentTool{echo},
				Context: &agent.AgentContext{},
				ToolCallHooks: agent.ToolCallHooks{
					AfterToolCall: func(_ context.Context, _ *agent.AfterToolCallContext) (*agent.AfterToolCallResult, error) {
						return after, nil
					},
				},
			})
			if tc.wantNil {
				if out.Result.StructuredContent != nil {
					t.Fatalf("%s: expected nil StructuredContent, got %+v", tc.name, out.Result.StructuredContent)
				}
			} else {
				obj, ok := out.Result.StructuredContent.(*jsonx.Object)
				if !ok {
					t.Fatalf("%s: expected *jsonx.Object, got %T", tc.name, out.Result.StructuredContent)
				}
				v, _ := obj.Get("value")
				if v != tc.wantValue {
					t.Fatalf("%s: expected %q, got %v", tc.name, tc.wantValue, v)
				}
			}
		}
	})

	t.Run("terminate batch semantics (#10549)", func(t *testing.T) {
		mkTool := func(termFirst bool) *agent.AgentTool {
			return &agent.AgentTool{
				Name:        "echo",
				Label:       "Echo",
				Description: "Echo",
				Parameters:  talSimpleSchema("value"),
				Execute: func(_ context.Context, _ string, params any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
					v, _ := params.(*jsonx.Object).Get("value")
					var term *bool
					if !termFirst || v == "first" {
						b := true
						term = &b
					}
					return agent.AgentToolResult{
						Content:   ai.ContentList{&ai.TextContent{Type: "text", Text: v.(string)}},
						Terminate: term,
					}, nil
				},
			}
		}

		// 1. Blocked call with BeforeToolCall.Terminate stops loop.
		calls := 0
		_, err := agent.RunAgentLoop(
			t.Context(),
			[]agent.AgentMessage{talCreateUserMessage("go")},
			&agent.AgentContext{Tools: []*agent.AgentTool{mkTool(false)}},
			&agent.AgentLoopConfig{
				Model:        talMockModel(),
				ConvertToLlm: talIdentityConvertToLlm,
				BeforeToolCall: func(_ context.Context, _ *agent.BeforeToolCallContext) (*agent.BeforeToolCallResult, error) {
					return &agent.BeforeToolCallResult{Block: true, Reason: "policy", Terminate: true}, nil
				},
			},
			nil,
			func(_ context.Context, _ *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
				calls++
				return talPushAssistantDone(talCreateAssistantMessage(ai.ContentList{
					&ai.ToolCall{Type: "toolCall", Id: "tc1", Name: "echo", Arguments: jsonx.ObjectOf("value", "first")},
				}, ai.StopReasonToolUse)), nil
			},
		)
		if err != nil || calls != 1 {
			t.Fatalf("blocked terminate expected 1 call, got calls=%d err=%v", calls, err)
		}

		// 2. Mixed batch with one terminating blocked call and one non-terminating call continues.
		calls = 0
		_, err = agent.RunAgentLoop(
			t.Context(),
			[]agent.AgentMessage{talCreateUserMessage("go")},
			&agent.AgentContext{Tools: []*agent.AgentTool{&agent.AgentTool{
				Name:        "echo",
				Label:       "Echo",
				Description: "Echo",
				Parameters:  talSimpleSchema("value"),
				Execute: func(_ context.Context, _ string, params any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
					v, _ := params.(*jsonx.Object).Get("value")
					return agent.AgentToolResult{Content: ai.ContentList{&ai.TextContent{Type: "text", Text: v.(string)}}}, nil
				},
			}}},
			&agent.AgentLoopConfig{
				Model:         talMockModel(),
				ToolExecution: agent.ToolExecutionModeParallel,
				ConvertToLlm:  talIdentityConvertToLlm,
				BeforeToolCall: func(_ context.Context, tc *agent.BeforeToolCallContext) (*agent.BeforeToolCallResult, error) {
					v, _ := tc.Args.(*jsonx.Object).Get("value")
					if v == "first" {
						return &agent.BeforeToolCallResult{Block: true, Reason: "policy", Terminate: true}, nil
					}
					return nil, nil
				},
			},
			nil,
			func(_ context.Context, _ *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
				calls++
				if calls == 1 {
					return talPushAssistantDone(talCreateAssistantMessage(ai.ContentList{
						&ai.ToolCall{Type: "toolCall", Id: "tc1", Name: "echo", Arguments: jsonx.ObjectOf("value", "first")},
						&ai.ToolCall{Type: "toolCall", Id: "tc2", Name: "echo", Arguments: jsonx.ObjectOf("value", "second")},
					}, ai.StopReasonToolUse)), nil
				}
				return talPushAssistantDone(talCreateAssistantMessage(ai.ContentList{&ai.TextContent{Type: "text", Text: "done"}}, ai.StopReasonStop)), nil
			},
		)
		if err != nil || calls != 2 {
			t.Fatalf("mixed blocked+unblocked expected 2 calls, got calls=%d err=%v", calls, err)
		}

		// 3. Parallel batch where only first sets Terminate continues.
		calls = 0
		_, err = agent.RunAgentLoop(
			t.Context(),
			[]agent.AgentMessage{talCreateUserMessage("go")},
			&agent.AgentContext{Tools: []*agent.AgentTool{mkTool(true)}},
			&agent.AgentLoopConfig{
				Model:         talMockModel(),
				ToolExecution: agent.ToolExecutionModeParallel,
				ConvertToLlm:  talIdentityConvertToLlm,
			},
			nil,
			func(_ context.Context, _ *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
				calls++
				if calls == 1 {
					return talPushAssistantDone(talCreateAssistantMessage(ai.ContentList{
						&ai.ToolCall{Type: "toolCall", Id: "tc1", Name: "echo", Arguments: jsonx.ObjectOf("value", "first")},
						&ai.ToolCall{Type: "toolCall", Id: "tc2", Name: "echo", Arguments: jsonx.ObjectOf("value", "second")},
					}, ai.StopReasonToolUse)), nil
				}
				return talPushAssistantDone(talCreateAssistantMessage(ai.ContentList{&ai.TextContent{Type: "text", Text: "done"}}, ai.StopReasonStop)), nil
			},
		)
		if err != nil || calls != 2 {
			t.Fatalf("partial parallel terminate expected 2 calls, got calls=%d err=%v", calls, err)
		}

		// 4. AfterToolCall setting Terminate=true stops batch.
		calls = 0
		termTrue := true
		_, err = agent.RunAgentLoop(
			t.Context(),
			[]agent.AgentMessage{talCreateUserMessage("go")},
			&agent.AgentContext{Tools: []*agent.AgentTool{&agent.AgentTool{
				Name:        "echo",
				Label:       "Echo",
				Description: "Echo",
				Parameters:  talSimpleSchema("value"),
				Execute: func(_ context.Context, _ string, _ any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
					return agent.AgentToolResult{Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "ok"}}}, nil
				},
			}}},
			&agent.AgentLoopConfig{
				Model:        talMockModel(),
				ConvertToLlm: talIdentityConvertToLlm,
				AfterToolCall: func(_ context.Context, _ *agent.AfterToolCallContext) (*agent.AfterToolCallResult, error) {
					return &agent.AfterToolCallResult{Terminate: &termTrue}, nil
				},
			},
			nil,
			func(_ context.Context, _ *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
				calls++
				return talPushAssistantDone(talCreateAssistantMessage(ai.ContentList{
					&ai.ToolCall{Type: "toolCall", Id: "tc1", Name: "echo", Arguments: jsonx.ObjectOf("value", "hello")},
				}, ai.StopReasonToolUse)), nil
			},
		)
		if err != nil || calls != 1 {
			t.Fatalf("AfterToolCall terminate expected 1 call, got calls=%d err=%v", calls, err)
		}
	})

	t.Run("RunAgentLoop suppresses settled tool updates even while sibling runs", func(t *testing.T) {
		var lateFire func()
		settlingEnded := make(chan struct{})
		lateDone := make(chan struct{})
		releaseSibling := make(chan struct{})

		settling := &agent.AgentTool{
			Name:        "settling",
			Label:       "Settling",
			Description: "Settling",
			Parameters:  talSimpleSchema(),
			Execute: func(_ context.Context, _ string, _ any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
				onUpdate(agent.AgentToolResult{Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "valid-update"}}})
				lateFire = func() {
					onUpdate(agent.AgentToolResult{Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "stale-update"}}})
					close(lateDone)
				}
				return agent.AgentToolResult{Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "done"}}}, nil
			},
		}
		sibling := &agent.AgentTool{
			Name:        "sibling",
			Label:       "Sibling",
			Description: "Sibling",
			Parameters:  talSimpleSchema(),
			Execute: func(_ context.Context, _ string, _ any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
				<-settlingEnded
				lateFire()
				<-lateDone
				close(releaseSibling)
				return agent.AgentToolResult{Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "sibling-done"}}}, nil
			},
		}

		call := 0
		var emitMu sync.Mutex
		var updateTexts []string
		_, err := agent.RunAgentLoop(
			t.Context(),
			[]agent.AgentMessage{talCreateUserMessage("go")},
			&agent.AgentContext{Tools: []*agent.AgentTool{settling, sibling}},
			&agent.AgentLoopConfig{
				Model:         talMockModel(),
				ToolExecution: agent.ToolExecutionModeParallel,
				ConvertToLlm:  talIdentityConvertToLlm,
			},
			func(ev agent.AgentEvent) {
				emitMu.Lock()
				defer emitMu.Unlock()
				switch e := ev.(type) {
				case *agent.AgentEventToolExecutionUpdate:
					res := e.PartialResult.(agent.AgentToolResult)
					updateTexts = append(updateTexts, res.Content[0].(*ai.TextContent).Text)
				case *agent.AgentEventToolExecutionEnd:
					if e.ToolCallId == "tc1" {
						close(settlingEnded)
					}
				}
			},
			func(_ context.Context, _ *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
				call++
				if call == 1 {
					return talPushAssistantDone(talCreateAssistantMessage(ai.ContentList{
						&ai.ToolCall{Type: "toolCall", Id: "tc1", Name: "settling", Arguments: jsonx.NewObject()},
						&ai.ToolCall{Type: "toolCall", Id: "tc2", Name: "sibling", Arguments: jsonx.NewObject()},
					}, ai.StopReasonToolUse)), nil
				}
				return talPushAssistantDone(talCreateAssistantMessage(ai.ContentList{&ai.TextContent{Type: "text", Text: "ok"}}, ai.StopReasonStop)), nil
			},
		)
		<-releaseSibling
		if err != nil {
			t.Fatalf("RunAgentLoop: %v", err)
		}
		if !reflect.DeepEqual(updateTexts, []string{"valid-update"}) {
			t.Fatalf("expected only [valid-update], got %v", updateTexts)
		}
	})
}

func TestAgentLoop_V152TurnLifecycleHooksParity(t *testing.T) {
	t.Run("FinishTurn runs after toolResult message_end and before turn_end", func(t *testing.T) {
		term := true
		tool := &agent.AgentTool{
			Name:        "echo",
			Label:       "Echo",
			Description: "Echo",
			Parameters:  talSimpleSchema(),
			Execute: func(_ context.Context, _ string, _ any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
				return agent.AgentToolResult{
					Content:   ai.ContentList{&ai.TextContent{Type: "text", Text: "ok"}},
					Terminate: &term,
				}, nil
			},
		}
		var ordering []string
		_, err := agent.RunAgentLoop(
			t.Context(),
			[]agent.AgentMessage{talCreateUserMessage("go")},
			&agent.AgentContext{Tools: []*agent.AgentTool{tool}},
			&agent.AgentLoopConfig{
				Model:        talMockModel(),
				ConvertToLlm: talIdentityConvertToLlm,
				FinishTurn: func(_ context.Context, tc *agent.AgentTurnContext) (*agent.AgentTurnDecision, error) {
					ordering = append(ordering, "finishTurn")
					if len(tc.ToolResults) != 1 {
						t.Fatalf("expected 1 toolResult, got %d", len(tc.ToolResults))
					}
					return nil, nil
				},
			},
			func(ev agent.AgentEvent) {
				switch e := ev.(type) {
				case *agent.AgentEventMessageEnd:
					ordering = append(ordering, "message_end:"+e.Message.GetRole())
				case *agent.AgentEventTurnEnd:
					ordering = append(ordering, "turn_end")
				}
			},
			func(_ context.Context, _ *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
				return talPushAssistantDone(talCreateAssistantMessage(ai.ContentList{
					&ai.ToolCall{Type: "toolCall", Id: "tc1", Name: "echo", Arguments: jsonx.NewObject()},
				}, ai.StopReasonToolUse)), nil
			},
		)
		if err != nil {
			t.Fatalf("RunAgentLoop: %v", err)
		}
		if len(ordering) < 3 || !reflect.DeepEqual(ordering[len(ordering)-3:], []string{"message_end:toolResult", "finishTurn", "turn_end"}) {
			t.Fatalf("unexpected ordering: %v", ordering)
		}
	})

	t.Run("FinishTurn runs on error and aborted before turn_end without overriding hard exit", func(t *testing.T) {
		for _, reason := range []ai.StopReason{ai.StopReasonError, ai.StopReasonAborted} {
			var ordering []string
			providerCalls := 0
			steeringPolls := 0
			followUpPolls := 0
			_, err := agent.RunAgentLoop(
				t.Context(),
				[]agent.AgentMessage{talCreateUserMessage("go")},
				&agent.AgentContext{},
				&agent.AgentLoopConfig{
					Model:        talMockModel(),
					ConvertToLlm: talIdentityConvertToLlm,
					FinishTurn: func(_ context.Context, tc *agent.AgentTurnContext) (*agent.AgentTurnDecision, error) {
						if tc.Message.StopReason != reason {
							t.Fatalf("expected StopReason %s, got %s", reason, tc.Message.StopReason)
						}
						ordering = append(ordering, "finishTurn")
						return &agent.AgentTurnDecision{Action: agent.AgentTurnDecisionContinue}, nil
					},
					GetSteeringMessages: func(context.Context) ([]agent.AgentMessage, error) {
						steeringPolls++
						return nil, nil
					},
					GetFollowUpMessages: func(context.Context) ([]agent.AgentMessage, error) {
						followUpPolls++
						return []agent.AgentMessage{talCreateUserMessage("queued")}, nil
					},
				},
				func(ev agent.AgentEvent) {
					if _, ok := ev.(*agent.AgentEventTurnEnd); ok {
						ordering = append(ordering, "turn_end")
					}
				},
				func(_ context.Context, _ *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
					providerCalls++
					return talPushAssistantError(talCreateAssistantMessage(ai.ContentList{}, reason, string(reason)), reason), nil
				},
			)
			if err != nil {
				t.Fatalf("%s RunAgentLoop: %v", reason, err)
			}
			if !reflect.DeepEqual(ordering, []string{"finishTurn", "turn_end"}) || providerCalls != 1 || steeringPolls != 1 || followUpPolls != 0 {
				t.Fatalf("%s mismatch: ordering=%v providerCalls=%d steeringPolls=%d followUpPolls=%d", reason, ordering, providerCalls, steeringPolls, followUpPolls)
			}
		}
	})

	t.Run("FinishTurn action:continue makes one context-only turn or yields to natural requests", func(t *testing.T) {
		providerCalls := 0
		finishCalls := 0
		_, err := agent.RunAgentLoop(
			t.Context(),
			[]agent.AgentMessage{talCreateUserMessage("go")},
			&agent.AgentContext{},
			&agent.AgentLoopConfig{
				Model:        talMockModel(),
				ConvertToLlm: talIdentityConvertToLlm,
				FinishTurn: func(_ context.Context, _ *agent.AgentTurnContext) (*agent.AgentTurnDecision, error) {
					finishCalls++
					if finishCalls == 1 {
						return &agent.AgentTurnDecision{Action: agent.AgentTurnDecisionContinue}, nil
					}
					return nil, nil
				},
			},
			nil,
			func(_ context.Context, _ *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
				providerCalls++
				return talPushAssistantDone(talCreateAssistantMessage(ai.ContentList{&ai.TextContent{Type: "text", Text: "resp"}}, ai.StopReasonStop)), nil
			},
		)
		if err != nil || providerCalls != 2 || finishCalls != 2 {
			t.Fatalf("context-only continuation expected 2 providerCalls/finishCalls, got %d/%d err=%v", providerCalls, finishCalls, err)
		}

		// Natural tool-result request satisfies continuation.
		tool := &agent.AgentTool{
			Name:        "noop",
			Label:       "Noop",
			Description: "Noop",
			Parameters:  talSimpleSchema(),
			Execute: func(_ context.Context, _ string, _ any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
				return agent.AgentToolResult{Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "done"}}}, nil
			},
		}
		providerCalls, finishCalls = 0, 0
		_, err = agent.RunAgentLoop(
			t.Context(),
			[]agent.AgentMessage{talCreateUserMessage("go")},
			&agent.AgentContext{Tools: []*agent.AgentTool{tool}},
			&agent.AgentLoopConfig{
				Model:        talMockModel(),
				ConvertToLlm: talIdentityConvertToLlm,
				FinishTurn: func(_ context.Context, _ *agent.AgentTurnContext) (*agent.AgentTurnDecision, error) {
					finishCalls++
					if finishCalls == 1 {
						return &agent.AgentTurnDecision{Action: agent.AgentTurnDecisionContinue}, nil
					}
					return nil, nil
				},
			},
			nil,
			func(_ context.Context, _ *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
				providerCalls++
				if providerCalls == 1 {
					return talPushAssistantDone(talCreateAssistantMessage(ai.ContentList{
						&ai.ToolCall{Type: "toolCall", Id: "tc1", Name: "noop", Arguments: jsonx.NewObject()},
					}, ai.StopReasonToolUse)), nil
				}
				return talPushAssistantDone(talCreateAssistantMessage(ai.ContentList{&ai.TextContent{Type: "text", Text: "done"}}, ai.StopReasonStop)), nil
			},
		)
		if err != nil || providerCalls != 2 || finishCalls != 2 {
			t.Fatalf("tool-result continuation expected 2/2, got %d/%d err=%v", providerCalls, finishCalls, err)
		}

		// Steering and follow-up each satisfy continuation without extra context-only request.
		for _, kind := range []string{"steering", "follow-up"} {
			providerCalls, finishCalls = 0, 0
			steeringPolls := 0
			followDelivered := false
			var secondUsers []string
			_, err = agent.RunAgentLoop(
				t.Context(),
				[]agent.AgentMessage{talCreateUserMessage("go")},
				&agent.AgentContext{},
				&agent.AgentLoopConfig{
					Model:        talMockModel(),
					ConvertToLlm: talIdentityConvertToLlm,
					FinishTurn: func(_ context.Context, _ *agent.AgentTurnContext) (*agent.AgentTurnDecision, error) {
						finishCalls++
						if finishCalls == 1 {
							return &agent.AgentTurnDecision{Action: agent.AgentTurnDecisionContinue}, nil
						}
						return nil, nil
					},
					GetSteeringMessages: func(context.Context) ([]agent.AgentMessage, error) {
						steeringPolls++
						if kind == "steering" && steeringPolls == 2 {
							return []agent.AgentMessage{talCreateUserMessage(kind)}, nil
						}
						return nil, nil
					},
					GetFollowUpMessages: func(context.Context) ([]agent.AgentMessage, error) {
						if kind != "follow-up" || followDelivered {
							return nil, nil
						}
						followDelivered = true
						return []agent.AgentMessage{talCreateUserMessage(kind)}, nil
					},
				},
				nil,
				func(_ context.Context, _ *ai.Model, tr *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
					providerCalls++
					if providerCalls == 2 {
						for _, m := range tr.Messages {
							if u, ok := m.(*ai.UserMessage); ok && len(u.Content.Blocks) > 0 {
								secondUsers = append(secondUsers, u.Content.Blocks[0].(*ai.TextContent).Text)
							}
						}
					}
					return talPushAssistantDone(talCreateAssistantMessage(ai.ContentList{&ai.TextContent{Type: "text", Text: "done"}}, ai.StopReasonStop)), nil
				},
			)
			if err != nil || providerCalls != 2 || finishCalls != 2 || !strings.Contains(strings.Join(secondUsers, ","), kind) {
				t.Fatalf("%s continuation failed: calls=%d finish=%d users=%v err=%v", kind, providerCalls, finishCalls, secondUsers, err)
			}
		}
	})

	t.Run("PrepareRequest does not re-poll steering and PrepareNextTurn picks up queued steering", func(t *testing.T) {
		var queued []agent.AgentMessage
		lateSteering := talCreateUserMessage("late-steering")
		var included []bool
		prepReqCalls := 0
		steeringPolls := 0

		_, err := agent.RunAgentLoop(
			t.Context(),
			[]agent.AgentMessage{talCreateUserMessage("go")},
			&agent.AgentContext{},
			&agent.AgentLoopConfig{
				Model:        talMockModel(),
				ConvertToLlm: talIdentityConvertToLlm,
				GetSteeringMessages: func(context.Context) ([]agent.AgentMessage, error) {
					steeringPolls++
					out := queued
					queued = nil
					return out, nil
				},
				PrepareRequest: func(_ context.Context, _ *agent.PrepareRequestContext) (*agent.AgentRequestUpdate, error) {
					prepReqCalls++
					if prepReqCalls == 1 {
						queued = append(queued, lateSteering)
					}
					return nil, nil
				},
			},
			nil,
			func(_ context.Context, _ *ai.Model, tr *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
				found := false
				for _, m := range tr.Messages {
					if m == lateSteering {
						found = true
					}
				}
				included = append(included, found)
				return talPushAssistantDone(talCreateAssistantMessage(ai.ContentList{&ai.TextContent{Type: "text", Text: "done"}}, ai.StopReasonStop)), nil
			},
		)
		if err != nil {
			t.Fatalf("RunAgentLoop: %v", err)
		}
		if !reflect.DeepEqual(included, []bool{false, true}) || prepReqCalls != 2 || steeringPolls != 3 {
			t.Fatalf("PrepareRequest steering poll check failed: included=%v prepReqCalls=%d steeringPolls=%d", included, prepReqCalls, steeringPolls)
		}

		// Steering queued during PrepareNextTurn is picked up before turn 2 without double polling.
		noop := &agent.AgentTool{
			Name:        "noop",
			Label:       "Noop",
			Description: "Noop",
			Parameters:  talSimpleSchema(),
			Execute: func(_ context.Context, _ string, _ any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
				return agent.AgentToolResult{Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "done"}}}, nil
			},
		}
		queued2 := []agent.AgentMessage{}
		late2 := talCreateUserMessage("queued-during-prepare")
		calls := 0
		secondIncluded := false
		_, err = agent.RunAgentLoop(
			t.Context(),
			[]agent.AgentMessage{talCreateUserMessage("go")},
			&agent.AgentContext{Tools: []*agent.AgentTool{noop}},
			&agent.AgentLoopConfig{
				Model:        talMockModel(),
				ConvertToLlm: talIdentityConvertToLlm,
				PrepareNextTurn: func(_ context.Context, _ *agent.PrepareNextTurnContext) (*agent.AgentLoopTurnUpdate, error) {
					queued2 = append(queued2, late2)
					return nil, nil
				},
				GetSteeringMessages: func(context.Context) ([]agent.AgentMessage, error) {
					out := queued2
					queued2 = nil
					return out, nil
				},
			},
			nil,
			func(_ context.Context, _ *ai.Model, tr *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
				calls++
				if calls == 2 {
					for _, m := range tr.Messages {
						if u, ok := m.(*ai.UserMessage); ok && len(u.Content.Blocks) > 0 && u.Content.Blocks[0].(*ai.TextContent).Text == "queued-during-prepare" {
							secondIncluded = true
						}
					}
					return talPushAssistantDone(talCreateAssistantMessage(ai.ContentList{&ai.TextContent{Type: "text", Text: "done"}}, ai.StopReasonStop)), nil
				}
				return talPushAssistantDone(talCreateAssistantMessage(ai.ContentList{
					&ai.ToolCall{Type: "toolCall", Id: "tc1", Name: "noop", Arguments: jsonx.NewObject()},
				}, ai.StopReasonToolUse)), nil
			},
		)
		if err != nil || calls != 2 || !secondIncluded {
			t.Fatalf("PrepareNextTurn late steering check failed: calls=%d secondIncluded=%v err=%v", calls, secondIncluded, err)
		}
	})
}
