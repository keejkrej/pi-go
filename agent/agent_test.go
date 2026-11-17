// Ported from packages/agent/test/agent.test.ts (pi v1.5.2).

package agent_test

import (
	"context"
	"errors"
	"net/http"
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
)

func tagentMockModel() *ai.Model {
	return &ai.Model{
		BaseModel: ai.BaseModel{
			Id:       "mock-agent-model",
			Name:     "Mock Agent Model",
			Api:      string(ai.KnownApiAnthropicMessages),
			Provider: ai.KnownProviderAnthropic,
			BaseUrl:  "https://mock.invalid",
			Input:    []ai.ModelModality{ai.ModelModalityText},
		},
		Reasoning:     true,
		ContextWindow: 200000,
		MaxTokens:     8192,
	}
}

func TestAgent_DefaultAndCustomInitialState(t *testing.T) {
	defaultAg := agent.NewAgent(nil)
	st := defaultAg.State()
	if st.Model == nil || st.Model.Id == "" {
		t.Fatalf("expected non-empty default model, got %+v", st.Model)
	}
	if st.ThinkingLevel != agent.ThinkingLevelOff {
		t.Fatalf("expected default thinking level off, got %q", st.ThinkingLevel)
	}
	if len(st.Tools) != 0 || len(st.Messages) != 0 || st.IsStreaming || len(st.PendingToolCalls) != 0 || st.ErrorMessage != nil {
		t.Fatalf("unexpected default state: %+v", st)
	}
	if defaultAg.Transport() != ai.TransportAuto {
		t.Fatalf("expected TransportAuto, got %q", defaultAg.Transport())
	}

	calc := testkit.CalculateTool()
	customAg := agent.NewAgent(&agent.AgentOptions{
		InitialState: &agent.AgentInitialState{
			SystemPrompt:  "You are helpful.",
			Model:         tagentMockModel(),
			ThinkingLevel: agent.ThinkingLevelLow,
			Tools:         []*agent.AgentTool{calc},
		},
	})
	cst := customAg.State()
	if cst.SystemPrompt != "You are helpful." || len(cst.Tools) != 1 || cst.ThinkingLevel != agent.ThinkingLevelLow {
		t.Fatalf("unexpected custom state: %+v", cst)
	}
	if len(cst.Messages) != 1 || cst.Messages[0].GetRole() != "system" {
		t.Fatalf("expected initial system message in transcript, got %+v", cst.Messages)
	}
}

func TestAgent_SystemPromptAndToolsDeltas(t *testing.T) {
	calc := testkit.CalculateTool()
	other := &agent.AgentTool{
		Name:        "ping",
		Label:       "Ping",
		Description: "Ping tool",
		Parameters:  talSimpleSchema(),
		Execute: func(_ context.Context, _ string, _ any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
			return agent.AgentToolResult{Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "pong"}}}, nil
		},
	}

	ag := agent.NewAgent(&agent.AgentOptions{
		InitialState: &agent.AgentInitialState{
			SystemPrompt: "initial prompt",
			Model:        tagentMockModel(),
			Tools:        []*agent.AgentTool{calc},
		},
	})

	ag.SetSystemPrompt("updated prompt")
	if ag.SystemPrompt() != "updated prompt" {
		t.Fatalf("expected updated system prompt, got %q", ag.SystemPrompt())
	}
	if len(ag.Messages()) != 1 {
		t.Fatalf("expected leading system message updated in place (len 1), got %d", len(ag.Messages()))
	}

	ag.SetTools([]*agent.AgentTool{calc, other})
	if got := len(ag.Tools()); got != 2 {
		t.Fatalf("expected 2 tools after SetTools, got %d", got)
	}
	if got := len(ag.Messages()); got != 1 {
		t.Fatalf("expected SetTools not to emit delta system message until next request, got len %d", got)
	}

	var seenCurrentTools []string
	ag.SetStreamFn(func(_ context.Context, _ *ai.Model, transcript *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
		for _, dt := range ai.GetCurrentTools(transcript.Messages) {
			seenCurrentTools = append(seenCurrentTools, dt.Name)
		}
		return talPushAssistantDone(talCreateAssistantMessage(
			ai.ContentList{&ai.TextContent{Type: "text", Text: "ok"}},
			ai.StopReasonStop,
		)), nil
	})
	if err := ag.PromptText(t.Context(), "check loadout"); err != nil {
		t.Fatalf("PromptText: %v", err)
	}
	if !reflect.DeepEqual(seenCurrentTools, []string{"calculate", "ping"}) {
		t.Fatalf("expected transcript tools [calculate ping], got %v", seenCurrentTools)
	}
	var deltaAdded string
	for _, m := range ag.Messages() {
		if sys, ok := m.(*ai.SystemMessage); ok && len(sys.ToolsAdded) == 1 && sys.ToolsAdded[0].Name == "ping" {
			deltaAdded = sys.ToolsAdded[0].Name
		}
	}
	if deltaAdded != "ping" {
		t.Fatalf("expected lazy delta system message adding ping tool, got %+v", ag.Messages())
	}

	// Clearing messages leaves empty slice while retaining agent config.
	ag.ClearMessages()
	if len(ag.Messages()) != 0 {
		t.Fatalf("expected empty messages after ClearMessages, got %d", len(ag.Messages()))
	}
}

func TestAgent_EmitsFullLifecycleEventsForThrownRunFailures(t *testing.T) {
	ag := agent.NewAgent(&agent.AgentOptions{
		InitialState: &agent.AgentInitialState{Model: tagentMockModel()},
		StreamFn: func(_ context.Context, _ *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
			return nil, errors.New("provider exploded")
		},
	})

	var events []agent.AgentEvent
	ag.Subscribe(func(ev agent.AgentEvent) {
		events = append(events, ev)
	})

	if err := ag.PromptText(t.Context(), "Hello"); err != nil {
		t.Fatalf("expected PromptText to convert run error into error message lifecycle, got %v", err)
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
		t.Fatalf("lifecycle event mismatch on thrown error:\ngot:  %v\nwant: %v", got, wantOrder)
	}

	st := ag.State()
	if st.IsStreaming {
		t.Fatalf("expected IsStreaming false")
	}
	if st.ErrorMessage == nil || *st.ErrorMessage != "provider exploded" {
		t.Fatalf("expected ErrorMessage 'provider exploded', got %v", st.ErrorMessage)
	}
	last, ok := st.Messages[len(st.Messages)-1].(*ai.AssistantMessage)
	if !ok || last.StopReason != ai.StopReasonError || last.ErrorMessage == nil || *last.ErrorMessage != "provider exploded" {
		t.Fatalf("unexpected error assistant message: %#v", st.Messages[len(st.Messages)-1])
	}
}

func TestAgent_OptionsForwardingSessionThinkingTransportAndRetryDelay(t *testing.T) {
	var seenSession *string
	var seenBudgets *ai.ThinkingBudgets
	var seenTransport *ai.Transport
	var seenRetryDelay *int64
	var seenOnProviderEvent bool

	cb := func(_ any, _ *ai.Model) error { return nil }
	transport := ai.TransportWebsocket
	delay := int64(7500)
	sid := "sess-agent-1"
	budgets := &ai.ThinkingBudgets{Medium: tproxyIntPtr(2048)}

	ag := agent.NewAgent(&agent.AgentOptions{
		InitialState: &agent.AgentInitialState{
			Model:         tagentMockModel(),
			ThinkingLevel: agent.ThinkingLevelMedium,
		},
		SessionId:             &sid,
		ThinkingBudgets:       budgets,
		Transport:             &transport,
		MaxRetryDelayMs:       &delay,
		OnProviderStreamEvent: cb,
		StreamFn: func(_ context.Context, m *ai.Model, _ *ai.TranscriptContext, opts *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
			seenSession = opts.SessionId
			seenBudgets = opts.ThinkingBudgets
			seenTransport = opts.Transport
			seenRetryDelay = opts.MaxRetryDelayMs
			seenOnProviderEvent = opts.OnProviderStreamEvent != nil
			return talPushAssistantDone(&ai.AssistantMessage{
				Role:       "assistant",
				Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: "ok"}},
				Api:        ai.Api(m.Api),
				Provider:   m.Provider,
				Model:      m.Id,
				StopReason: ai.StopReasonStop,
				Timestamp:  time.Now().UnixMilli(),
			}), nil
		},
	})

	if err := ag.PromptText(t.Context(), "test options"); err != nil {
		t.Fatalf("PromptText: %v", err)
	}
	if seenSession == nil || *seenSession != sid {
		t.Fatalf("expected sessionId %q, got %v", sid, seenSession)
	}
	if seenBudgets == nil || seenBudgets.Medium == nil || *seenBudgets.Medium != 2048 {
		t.Fatalf("expected thinkingBudgets forwarded, got %+v", seenBudgets)
	}
	if seenTransport == nil || *seenTransport != ai.TransportWebsocket {
		t.Fatalf("expected websocket transport forwarded, got %v", seenTransport)
	}
	if seenRetryDelay == nil || *seenRetryDelay != 7500 {
		t.Fatalf("expected MaxRetryDelayMs 7500 forwarded, got %v", seenRetryDelay)
	}
	if !seenOnProviderEvent {
		t.Fatalf("expected OnProviderStreamEvent callback forwarded")
	}
}

func TestAgent_QueuesSteeringFollowUpAndGuards(t *testing.T) {
	t.Run("Abort while idle is safe; Reset while streaming rejects without corrupting state", func(t *testing.T) {
		started := make(chan struct{})
		release := make(chan struct{})

		ag := agent.NewAgent(&agent.AgentOptions{
			InitialState: &agent.AgentInitialState{Model: tagentMockModel()},
			StreamFn: func(ctx context.Context, m *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
				s := ai.NewAssistantMessageEventStream()
				go func() {
					partial := &ai.AssistantMessage{
						Role:       "assistant",
						Content:    ai.ContentList{},
						Api:        ai.Api(m.Api),
						Provider:   m.Provider,
						Model:      m.Id,
						StopReason: ai.StopReasonStop,
						Timestamp:  time.Now().UnixMilli(),
					}
					s.Push(&ai.AssistantMessageEventStart{Type: "start", Partial: partial})
					close(started)
					select {
					case <-release:
					case <-ctx.Done():
					}
					final := &ai.AssistantMessage{
						Role:       "assistant",
						Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: "done"}},
						Api:        ai.Api(m.Api),
						Provider:   m.Provider,
						Model:      m.Id,
						StopReason: ai.StopReasonStop,
						Timestamp:  partial.Timestamp,
					}
					s.Push(&ai.AssistantMessageEventDone{Type: "done", Reason: ai.StopReasonStop, Message: final})
					s.End(final)
				}()
				return s, nil
			},
		})

		ag.Abort() // Idle abort must not fail.

		errCh := make(chan error, 1)
		go func() {
			errCh <- ag.PromptText(t.Context(), "streaming...")
		}()

		<-started
		if ag.Signal() == nil {
			t.Fatalf("expected Signal() non-nil while streaming")
		}
		if err := ag.Reset(); err == nil || !strings.Contains(err.Error(), "already processing") {
			t.Fatalf("expected Reset to fail while streaming, got %v", err)
		}
		if err := ag.PromptText(t.Context(), "second"); err == nil || !strings.Contains(err.Error(), "already processing") {
			t.Fatalf("expected second PromptText to fail while streaming, got %v", err)
		}
		if err := ag.Continue(t.Context()); err == nil || !strings.Contains(err.Error(), "already processing") {
			t.Fatalf("expected Continue to fail while streaming, got %v", err)
		}

		close(release)
		if err := ag.WaitForIdle(t.Context()); err != nil {
			t.Fatalf("WaitForIdle: %v", err)
		}
		if err := <-errCh; err != nil {
			t.Fatalf("PromptText: %v", err)
		}
		if len(ag.Messages()) != 2 {
			t.Fatalf("expected intact transcript (len 2), got %d", len(ag.Messages()))
		}
		if ag.Signal() != nil {
			t.Fatalf("expected Signal() nil when idle")
		}
		if err := ag.Reset(); err != nil {
			t.Fatalf("Reset when idle failed: %v", err)
		}
		if len(ag.Messages()) != 0 {
			t.Fatalf("expected empty messages after Reset")
		}
	})

	t.Run("Continue rejects empty or system-only context without draining queues", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			init *agent.AgentInitialState
		}{
			{
				name: "empty transcript",
				init: &agent.AgentInitialState{Model: tagentMockModel()},
			},
			{
				name: "system-only transcript",
				init: &agent.AgentInitialState{SystemPrompt: "You are helpful", Model: tagentMockModel()},
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				ag := agent.NewAgent(&agent.AgentOptions{
					InitialState: tc.init,
					StreamFn: func(_ context.Context, _ *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
						t.Fatalf("StreamFn must not be called")
						return nil, nil
					},
				})
				steerMsg := talCreateUserMessage("queued steer")
				followMsg := talCreateUserMessage("queued follow")
				ag.Steer(steerMsg)
				ag.FollowUp(followMsg)

				err := ag.Continue(t.Context())
				if err == nil || !strings.Contains(err.Error(), "No messages to continue from") {
					t.Fatalf("expected No messages error, got %v", err)
				}
				if !ag.HasQueuedMessages() {
					t.Fatalf("expected queues NOT drained on rejected Continue")
				}
				peek := ag.PeekQueuedMessages()
				if len(peek.Steering) != 1 || len(peek.FollowUp) != 1 {
					t.Fatalf("expected both queues preserved, got %+v", peek)
				}
			})
		}
	})

	t.Run("Continue drains one-at-a-time steering/followUp from trailing assistant", func(t *testing.T) {
		var promptsSeen []string
		ag := agent.NewAgent(&agent.AgentOptions{
			InitialState: &agent.AgentInitialState{
				Model: tagentMockModel(),
				Messages: []agent.AgentMessage{
					talCreateUserMessage("initial"),
					talCreateAssistantMessage(ai.ContentList{&ai.TextContent{Type: "text", Text: "first reply"}}, ai.StopReasonStop),
				},
			},
			SteeringMode: agent.QueueModeOneAtATime,
			FollowUpMode: agent.QueueModeOneAtATime,
			StreamFn: func(_ context.Context, _ *ai.Model, transcript *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
				for _, m := range transcript.Messages {
					if u, ok := m.(*ai.UserMessage); ok && len(u.Content.Blocks) > 0 {
						promptsSeen = append(promptsSeen, u.Content.Blocks[0].(*ai.TextContent).Text)
					}
				}
				return talPushAssistantDone(talCreateAssistantMessage(
					ai.ContentList{&ai.TextContent{Type: "text", Text: "continued"}},
					ai.StopReasonStop,
				)), nil
			},
		})

		ag.Steer(talCreateUserMessage("steer-1"))
		ag.Steer(talCreateUserMessage("steer-2"))
		ag.FollowUp(talCreateUserMessage("follow-1"))

		// Continue dequeues 1 steering message ("steer-1"), leaving "steer-2" for steering check
		// during the run (which drains steer-2 too before stopping!), or wait: let's observe!
		if err := ag.Continue(t.Context()); err != nil {
			t.Fatalf("Continue: %v", err)
		}
		if ag.HasQueuedMessages() {
			t.Fatalf("expected all queued messages consumed across loop turns, peek=%+v", ag.PeekQueuedMessages())
		}
		wantTexts := []string{"steer-1", "steer-2", "follow-1"}
		for _, want := range wantTexts {
			if !strings.Contains(strings.Join(promptsSeen, ","), want) {
				t.Fatalf("expected %q seen in prompts, got %v", want, promptsSeen)
			}
		}
	})
}

func TestAgent_FinishTurnAndPrepareNextTurnSyncState(t *testing.T) {
	model2 := tagentMockModel()
	model2.Id = "model-v2"
	tool2 := testkit.CalculateTool()

	var seenTurn2Model string
	var seenTurn2Reasoning *ai.ThinkingLevel
	var seenTurn2Tools []string
	var seenTurn2SysPrompt string
	turn := 0
	var ag *agent.Agent
	ag = agent.NewAgent(&agent.AgentOptions{
		InitialState: &agent.AgentInitialState{
			SystemPrompt: "v1",
			Model:        tagentMockModel(),
		},
		PrepareNextTurnWithContext: func(c context.Context, next *agent.PrepareNextTurnContext) (*agent.AgentLoopTurnUpdate, error) {
			if c.Err() != nil {
				t.Fatalf("expected active context in PrepareNextTurnWithContext: %v", c.Err())
			}
			if next.Message == nil || len(next.ToolResults) != 1 {
				t.Fatalf("expected assistant message and 1 toolResult in PrepareNextTurnContext, got %+v", next)
			}
			r := agent.ThinkingLevelHigh
			sys := "v2"
			return &agent.AgentLoopTurnUpdate{
				Context: &agent.AgentContext{
					Messages: agent.AgentMessageList{ai.CreateInitialSystemMessage(&sys, nil)},
					Tools:    []*agent.AgentTool{tool2},
				},
				Messages:      append(agent.AgentMessageList(nil), next.Context.Messages[1:]...),
				Model:         model2,
				ThinkingLevel: &r,
			}, nil
		},
		FinishTurn: func(c context.Context, tc *agent.AgentTurnContext) (*agent.AgentTurnDecision, error) {
			if c.Err() != nil {
				t.Fatalf("expected active run context in FinishTurn: %v", c.Err())
			}
			if tc.Context == nil || len(tc.Context.Messages) == 0 {
				t.Fatalf("expected populated AgentTurnContext")
			}
			if turn >= 2 {
				return &agent.AgentTurnDecision{Action: agent.AgentTurnDecisionEnd}, nil
			}
			return &agent.AgentTurnDecision{Action: agent.AgentTurnDecisionContinue}, nil
		},
		StreamFn: func(_ context.Context, m *ai.Model, transcript *ai.TranscriptContext, opts *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
			turn++
			if turn == 1 {
				return talPushAssistantDone(&ai.AssistantMessage{
					Role: "assistant",
					Content: ai.ContentList{
						&ai.ToolCall{Type: "toolCall", Id: "t1", Name: "missing", Arguments: jsonx.NewObject()},
					},
					Api:        ai.Api(m.Api),
					Provider:   m.Provider,
					Model:      m.Id,
					StopReason: ai.StopReasonToolUse,
					Timestamp:  time.Now().UnixMilli(),
				}), nil
			}
			seenTurn2Model = m.Id
			seenTurn2Reasoning = opts.Reasoning
			seenTurn2SysPrompt = ai.GetCurrentSystemPrompt(transcript.Messages)
			for _, dt := range ai.GetCurrentTools(transcript.Messages) {
				seenTurn2Tools = append(seenTurn2Tools, dt.Name)
			}
			return talPushAssistantDone(&ai.AssistantMessage{
				Role:       "assistant",
				Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: "turn 2"}},
				Api:        ai.Api(m.Api),
				Provider:   m.Provider,
				Model:      m.Id,
				StopReason: ai.StopReasonStop,
				Timestamp:  time.Now().UnixMilli(),
			}), nil
		},
	})

	if err := ag.PromptText(t.Context(), "go"); err != nil {
		t.Fatalf("PromptText: %v", err)
	}
	if seenTurn2Model != "model-v2" {
		t.Fatalf("expected turn 2 model 'model-v2', got %q", seenTurn2Model)
	}
	if seenTurn2Reasoning == nil || *seenTurn2Reasoning != ai.ThinkingLevelHigh {
		t.Fatalf("expected turn 2 reasoning 'high', got %v", seenTurn2Reasoning)
	}
	if seenTurn2SysPrompt != "v2" {
		t.Fatalf("expected turn 2 system prompt 'v2', got %q", seenTurn2SysPrompt)
	}
	if !reflect.DeepEqual(seenTurn2Tools, []string{"calculate"}) {
		t.Fatalf("expected turn 2 tools [calculate], got %v", seenTurn2Tools)
	}
	if turn != 2 || ag.State().IsStreaming {
		t.Fatalf("expected 2 turns and idle state, turn=%d isStreaming=%v", turn, ag.State().IsStreaming)
	}
}

// Tests docs/porting.md §7.3: mutexes never held across listener callbacks, tool execution,
// or stream consumption; concurrent state reads and listener re-entrancy must not deadlock or race.
func TestAgent_ThreadSafetyUnlockedListenersAndConcurrentReaders(t *testing.T) {
	toolStarted := make(chan struct{})
	releaseTool := make(chan struct{})

	slowTool := &agent.AgentTool{
		Name:        "slow",
		Label:       "Slow",
		Description: "Slow tool",
		Parameters:  talSimpleSchema(),
		Execute: func(_ context.Context, _ string, _ any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
			close(toolStarted)
			onUpdate(agent.AgentToolResult{Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "progress"}}})
			<-releaseTool
			return agent.AgentToolResult{Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "ok"}}}, nil
		},
	}

	turn := 0
	ag := agent.NewAgent(&agent.AgentOptions{
		InitialState: &agent.AgentInitialState{
			Model: tagentMockModel(),
			Tools: []*agent.AgentTool{slowTool},
		},
		StreamFn: func(_ context.Context, m *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
			turn++
			if turn == 1 {
				return talPushAssistantDone(&ai.AssistantMessage{
					Role: "assistant",
					Content: ai.ContentList{
						&ai.ToolCall{Type: "toolCall", Id: "tc-slow", Name: "slow", Arguments: jsonx.NewObject()},
					},
					Api:        ai.Api(m.Api),
					Provider:   m.Provider,
					Model:      m.Id,
					StopReason: ai.StopReasonToolUse,
					Timestamp:  time.Now().UnixMilli(),
				}), nil
			}
			return talPushAssistantDone(&ai.AssistantMessage{
				Role:       "assistant",
				Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: "finished"}},
				Api:        ai.Api(m.Api),
				Provider:   m.Provider,
				Model:      m.Id,
				StopReason: ai.StopReasonStop,
				Timestamp:  time.Now().UnixMilli(),
			}), nil
		},
	})

	// Re-entrant subscriber calling State(), PendingToolCalls(), and Unsubscribe() inside callback!
	var unsub func()
	var unsubOnce sync.Once
	reentrantStateReads := 0
	unsub = ag.Subscribe(func(ev agent.AgentEvent) {
		_ = ag.State()
		_ = ag.PendingToolCalls()
		reentrantStateReads++
		if _, ok := ev.(*agent.AgentEventToolExecutionUpdate); ok {
			unsubOnce.Do(func() { unsub() })
		}
	})

	runDone := make(chan error, 1)
	go func() {
		runDone <- ag.PromptText(t.Context(), "go")
	}()

	<-toolStarted

	// Hammer State()/Messages()/HasQueuedMessages() concurrently while tool is running.
	var readerWg sync.WaitGroup
	for range 8 {
		readerWg.Add(1)
		go func() {
			defer readerWg.Done()
			for range 50 {
				st := ag.State()
				_ = len(st.Messages)
				_ = ag.HasQueuedMessages()
				_ = ag.PeekQueuedMessages()
			}
		}()
	}
	readerWg.Wait()
	close(releaseTool)

	if err := <-runDone; err != nil {
		t.Fatalf("PromptText: %v", err)
	}
	if reentrantStateReads == 0 {
		t.Fatalf("expected re-entrant subscriber callbacks to execute cleanly")
	}
}

func TestAgent_TestkitCalculateAndEventDecode(t *testing.T) {
	val, err := testkit.Calculate("-(10 + 5) * 2 / 3")
	if err != nil || val != -10 {
		t.Fatalf("expected -10, got %v (err=%v)", val, err)
	}
	if _, err := testkit.Calculate("1 / 0"); err == nil {
		t.Fatalf("expected division by zero error")
	}
	if _, err := testkit.Calculate("2 + * 3"); err == nil {
		t.Fatalf("expected syntax error")
	}

	toolUsage := ai.Usage{Input: 7, Output: 11, TotalTokens: 18}
	calcWithUsage := testkit.CreateCalculateToolWithUsage(toolUsage)
	out := agent.RunToolCall(
		t.Context(),
		&agent.AgentToolCall{Type: "toolCall", Id: "c1", Name: "calculate", Arguments: jsonx.ObjectOf("expression", "6 * 7")},
		&agent.RunToolCallOptions{
			Tools:   []*agent.AgentTool{calcWithUsage},
			Context: &agent.AgentContext{},
		},
	)
	if out.IsError || out.Result.Usage == nil || out.Result.Usage.TotalTokens != 18 {
		t.Fatalf("expected tool usage TotalTokens=18, got %+v", out)
	}
	tr := &ai.ToolResultMessage{
		Role:       "toolResult",
		ToolCallId: out.ToolCall.Id,
		ToolName:   out.ToolCall.Name,
		Content:    out.Result.Content,
		Details:    jsonx.Some(out.Result.Details),
		Usage:      out.Result.Usage,
		IsError:    out.IsError,
		DurationMs: out.DurationMs,
		Timestamp:  time.Now().UnixMilli(),
	}

	user := talCreateUserMessage("hi")
	assistant := talCreateAssistantMessage(ai.ContentList{&ai.TextContent{Type: "text", Text: "yo"}}, ai.StopReasonStop)
	events := []agent.AgentEvent{
		&agent.AgentEventAgentStart{Type: "agent_start"},
		&agent.AgentEventTurnStart{Type: "turn_start"},
		&agent.AgentEventMessageStart{Type: "message_start", Message: user},
		&agent.AgentEventMessageUpdate{
			Type:                  "message_update",
			Message:               assistant,
			AssistantMessageEvent: &ai.AssistantMessageEventTextDelta{Type: "text_delta", ContentIndex: 0, Delta: "yo", Partial: assistant},
		},
		&agent.AgentEventMessageEnd{Type: "message_end", Message: assistant},
		&agent.AgentEventToolExecutionStart{Type: "tool_execution_start", ToolCallId: "t1", ToolName: "calc", Args: jsonx.ObjectOf("x", 1)},
		&agent.AgentEventToolExecutionUpdate{Type: "tool_execution_update", ToolCallId: "t1", ToolName: "calc", Args: jsonx.ObjectOf("x", 1), PartialResult: "50%"},
		&agent.AgentEventToolExecutionEnd{Type: "tool_execution_end", ToolCallId: "t1", ToolName: "calc", Result: "done", IsError: false},
		&agent.AgentEventTurnEnd{Type: "turn_end", Message: assistant, ToolResults: []*ai.ToolResultMessage{tr}},
		&agent.AgentEventAgentEnd{Type: "agent_end", Messages: agent.AgentMessageList{user, assistant}},
	}
	for i, ev := range events {
		b, err := jsonx.Marshal(ev)
		if err != nil {
			t.Fatalf("marshal event %d (%T): %v", i, ev, err)
		}
		decoded, err := agent.UnmarshalAgentEvent(b)
		if err != nil {
			t.Fatalf("unmarshal event %d (%T): %v", i, ev, err)
		}
		if reflect.TypeOf(decoded) != reflect.TypeOf(ev) {
			t.Fatalf("event %d type mismatch: got %T, want %T", i, decoded, ev)
		}
	}

	unkEv, err := agent.UnmarshalAgentEvent([]byte(`{"type":"future_event","x":1}`))
	if err != nil {
		t.Fatalf("unmarshal unknown event: %v", err)
	}
	if _, ok := unkEv.(*agent.UnknownAgentEvent); !ok {
		t.Fatalf("expected *UnknownAgentEvent, got %T", unkEv)
	}
}

func TestAgent_V152TranscriptLoadoutAndReset(t *testing.T) {
	mkTool := func(name string) *agent.AgentTool {
		return &agent.AgentTool{
			Name:        name,
			Label:       name,
			Description: name + " tool",
			Parameters:  talSimpleSchema(),
			Execute: func(_ context.Context, _ string, _ any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
				return agent.AgentToolResult{Content: ai.ContentList{&ai.TextContent{Type: "text", Text: name}}}, nil
			},
		}
	}

	t.Run("declares tool changes lazily before next request and resets baseline", func(t *testing.T) {
		first := mkTool("first")
		second := mkTool("second")
		var requests [][]string

		ag := agent.NewAgent(&agent.AgentOptions{
			InitialState: &agent.AgentInitialState{
				SystemPrompt: "You are helpful.",
				Model:        tagentMockModel(),
				Tools:        []*agent.AgentTool{first},
				Messages:     []agent.AgentMessage{talCreateUserMessage("old")},
			},
			StreamFn: func(_ context.Context, _ *ai.Model, tr *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
				var sig []string
				for _, m := range tr.Messages {
					if sys, ok := m.(*ai.SystemMessage); ok {
						var added, removed []string
						for _, a := range sys.ToolsAdded {
							added = append(added, a.Name)
						}
						for _, r := range sys.ToolsRemoved {
							removed = append(removed, r.Name)
						}
						sig = append(sig, "+"+strings.Join(added, ","), "-"+strings.Join(removed, ","))
					}
				}
				requests = append(requests, sig)
				return talPushAssistantDone(talCreateAssistantMessage(ai.ContentList{&ai.TextContent{Type: "text", Text: "done"}}, ai.StopReasonStop)), nil
			},
		})

		if err := ag.Reset(); err != nil {
			t.Fatalf("Reset: %v", err)
		}
		msgs := ag.Messages()
		if len(msgs) != 1 {
			t.Fatalf("expected Reset to restore single baseline SystemMessage, got len=%d", len(msgs))
		}
		sys0 := msgs[0].(*ai.SystemMessage)
		if *sys0.Content.Text != "You are helpful." || len(sys0.ToolsAdded) != 1 || sys0.ToolsAdded[0].Name != "first" {
			t.Fatalf("unexpected baseline system message: %+v", sys0)
		}

		if err := ag.PromptText(t.Context(), "one"); err != nil {
			t.Fatalf("PromptText one: %v", err)
		}
		ag.SetTools([]*agent.AgentTool{second})
		if err := ag.PromptText(t.Context(), "two"); err != nil {
			t.Fatalf("PromptText two: %v", err)
		}
		if err := ag.PromptText(t.Context(), "three"); err != nil {
			t.Fatalf("PromptText three: %v", err)
		}

		want := [][]string{
			{"+first", "-"},
			{"+first", "-", "+second", "-first"},
			{"+first", "-", "+second", "-first"},
		}
		if !reflect.DeepEqual(requests, want) {
			t.Fatalf("tool change declaration mismatch:\ngot:  %v\nwant: %v", requests, want)
		}
	})

	t.Run("merges tool changes into pending SystemMessage and rewrites stale pending declarations", func(t *testing.T) {
		echo := mkTool("echo")
		var seenSysCount int
		ag := agent.NewAgent(&agent.AgentOptions{
			InitialState: &agent.AgentInitialState{
				SystemPrompt: "You are helpful.",
				Model:        tagentMockModel(),
			},
			StreamFn: func(_ context.Context, _ *ai.Model, tr *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
				seenSysCount = 0
				for _, m := range tr.Messages {
					if m.GetRole() == "system" {
						seenSysCount++
					}
				}
				return talPushAssistantDone(talCreateAssistantMessage(ai.ContentList{&ai.TextContent{Type: "text", Text: "done"}}, ai.StopReasonStop)), nil
			},
		})
		ag.SetTools([]*agent.AgentTool{echo})
		empty := ""
		sec := "<skills>x</skills>"
		sections := omap.NewMap[string, *string]()
		sections.Set("skills", &sec)
		pendingSys := &ai.SystemMessage{
			Role:      "system",
			Content:   ai.SystemMessageContent{Text: &empty},
			Sections:  sections,
			Timestamp: 1,
		}
		if err := ag.Prompt(t.Context(), pendingSys, talCreateUserMessage("hi")); err != nil {
			t.Fatalf("Prompt pending SystemMessage: %v", err)
		}
		if seenSysCount != 2 {
			t.Fatalf("expected 2 system messages in transcript, got %d", seenSysCount)
		}
		merged := ag.Messages()[1].(*ai.SystemMessage)
		if merged.Sections == nil || len(merged.ToolsAdded) != 1 || merged.ToolsAdded[0].Name != "echo" {
			t.Fatalf("expected pending system message merged with +echo, got %+v", merged)
		}

		// Rewrites stale declarations on a pending system message to match executable set.
		first := mkTool("first")
		second := mkTool("second")
		ag2 := agent.NewAgent(&agent.AgentOptions{
			InitialState: &agent.AgentInitialState{
				SystemPrompt: "You are helpful.",
				Model:        tagentMockModel(),
				Tools:        []*agent.AgentTool{first},
			},
			StreamFn: func(_ context.Context, _ *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
				return talPushAssistantDone(talCreateAssistantMessage(ai.ContentList{&ai.TextContent{Type: "text", Text: "done"}}, ai.StopReasonStop)), nil
			},
		})
		note := "<note>x</note>"
		sec2 := omap.NewMap[string, *string]()
		sec2.Set("note", &note)
		staleSys := &ai.SystemMessage{
			Role:         "system",
			Content:      ai.SystemMessageContent{Text: &empty},
			Sections:     sec2,
			ToolsAdded:   []*ai.Tool{second.AiTool()},
			ToolsRemoved: []ai.ToolReference{{Name: "first"}},
			Timestamp:    1,
		}
		if err := ag2.Prompt(t.Context(), staleSys, talCreateUserMessage("hi")); err != nil {
			t.Fatalf("Prompt stale SystemMessage: %v", err)
		}
		rewritten := ag2.Messages()[1].(*ai.SystemMessage)
		if len(rewritten.ToolsAdded) != 0 || len(rewritten.ToolsRemoved) != 0 {
			t.Fatalf("expected stale tool declarations stripped, got %+v", rewritten)
		}
		curSys := ai.GetCurrentSystemMessage(agent.DefaultConvertToLlm(ag2.Messages()))
		if curSys == nil || len(curSys.ToolsAdded) != 1 || curSys.ToolsAdded[0].Name != "first" {
			t.Fatalf("expected executable set [first] preserved, got %+v", curSys)
		}
	})
}

func TestAgent_V152LifecycleQueuesAndHooks(t *testing.T) {
	t.Run("isStreaming stays true during agent_end subscriber and WaitForIdle waits", func(t *testing.T) {
		endEntered := make(chan struct{})
		releaseEnd := make(chan struct{})
		var idleReturned atomic.Bool

		ag := agent.NewAgent(&agent.AgentOptions{
			InitialState: &agent.AgentInitialState{Model: tagentMockModel()},
			StreamFn: func(_ context.Context, _ *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
				return talPushAssistantDone(talCreateAssistantMessage(ai.ContentList{&ai.TextContent{Type: "text", Text: "ok"}}, ai.StopReasonStop)), nil
			},
		})
		var endSigActive bool
		ag.Subscribe(func(ev agent.AgentEvent) {
			if _, ok := ev.(*agent.AgentEventAgentEnd); ok {
				sig := ag.Signal()
				endSigActive = sig != nil && sig.Err() == nil
				close(endEntered)
				<-releaseEnd
			}
		})

		ctx := t.Context()
		doneCh := make(chan error, 1)
		go func() {
			doneCh <- ag.PromptText(ctx, "hello")
		}()

		<-endEntered
		waiterDone := make(chan struct{})
		go func() {
			_ = ag.WaitForIdle(ctx)
			idleReturned.Store(true)
			close(waiterDone)
		}()
		time.Sleep(15 * time.Millisecond)

		if !endSigActive {
			t.Fatalf("expected Signal() active during agent_end subscriber")
		}
		if !ag.State().IsStreaming {
			t.Fatalf("expected IsStreaming=true while agent_end subscriber is running")
		}
		if idleReturned.Load() {
			t.Fatalf("expected WaitForIdle to block while agent_end subscriber is running")
		}

		close(releaseEnd)
		if err := <-doneCh; err != nil {
			t.Fatalf("PromptText: %v", err)
		}
		<-waiterDone
		if err := ag.WaitForIdle(ctx); err != nil {
			t.Fatalf("WaitForIdle: %v", err)
		}
		if !idleReturned.Load() || ag.State().IsStreaming {
			t.Fatalf("expected WaitForIdle done and IsStreaming=false after subscriber finishes")
		}
	})

	t.Run("Continue keeps one-at-a-time vs all steering semantics from assistant and non-assistant tails", func(t *testing.T) {
		for _, tc := range []struct {
			mode         agent.QueueMode
			wantRequests int
		}{
			{mode: agent.QueueModeOneAtATime, wantRequests: 2},
			{mode: agent.QueueModeAll, wantRequests: 1},
		} {
			// Assistant-tail fallback:
			var reqUsers [][]string
			ag := agent.NewAgent(&agent.AgentOptions{
				InitialState: &agent.AgentInitialState{
					Model: tagentMockModel(),
					Messages: []agent.AgentMessage{
						talCreateUserMessage("Initial"),
						talCreateAssistantMessage(ai.ContentList{&ai.TextContent{Type: "text", Text: "Reply"}}, ai.StopReasonStop),
					},
				},
				SteeringMode: tc.mode,
				StreamFn: func(_ context.Context, _ *ai.Model, tr *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
					var users []string
					for _, m := range tr.Messages {
						if u, ok := m.(*ai.UserMessage); ok && len(u.Content.Blocks) > 0 {
							users = append(users, u.Content.Blocks[0].(*ai.TextContent).Text)
						}
					}
					reqUsers = append(reqUsers, users)
					return talPushAssistantDone(talCreateAssistantMessage(ai.ContentList{&ai.TextContent{Type: "text", Text: "ok"}}, ai.StopReasonStop)), nil
				},
			})
			ag.Steer(talCreateUserMessage("Steering 1"))
			ag.Steer(talCreateUserMessage("Steering 2"))
			if err := ag.Continue(t.Context()); err != nil {
				t.Fatalf("assistant-tail %s Continue: %v", tc.mode, err)
			}
			if len(reqUsers) != tc.wantRequests {
				t.Fatalf("assistant-tail %s expected %d requests, got %d (%v)", tc.mode, tc.wantRequests, len(reqUsers), reqUsers)
			}
			if tc.mode == agent.QueueModeOneAtATime {
				if strings.Contains(strings.Join(reqUsers[0], ","), "Steering 2") || !strings.Contains(strings.Join(reqUsers[1], ","), "Steering 2") {
					t.Fatalf("one-at-a-time assistant-tail failed: %v", reqUsers)
				}
			}

			// Non-assistant user-tail startup steering + deferred follow-up:
			reqUsers = nil
			ag2 := agent.NewAgent(&agent.AgentOptions{
				InitialState: &agent.AgentInitialState{
					Model:    tagentMockModel(),
					Messages: []agent.AgentMessage{talCreateUserMessage("existing")},
				},
				SteeringMode: tc.mode,
				StreamFn: func(_ context.Context, _ *ai.Model, tr *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
					var users []string
					for _, m := range tr.Messages {
						if u, ok := m.(*ai.UserMessage); ok && len(u.Content.Blocks) > 0 {
							users = append(users, u.Content.Blocks[0].(*ai.TextContent).Text)
						}
					}
					reqUsers = append(reqUsers, users)
					return talPushAssistantDone(talCreateAssistantMessage(ai.ContentList{&ai.TextContent{Type: "text", Text: "ok"}}, ai.StopReasonStop)), nil
				},
			})
			ag2.Steer(talCreateUserMessage("first"))
			ag2.Steer(talCreateUserMessage("second"))
			if err := ag2.Continue(t.Context()); err != nil {
				t.Fatalf("user-tail %s Continue: %v", tc.mode, err)
			}
			if len(reqUsers) != tc.wantRequests {
				t.Fatalf("user-tail %s expected %d requests, got %d (%v)", tc.mode, tc.wantRequests, len(reqUsers), reqUsers)
			}
		}

		// Steering stays ahead of follow-up from non-assistant tail.
		var usersByReq [][]string
		ag3 := agent.NewAgent(&agent.AgentOptions{
			InitialState: &agent.AgentInitialState{
				Model:    tagentMockModel(),
				Messages: []agent.AgentMessage{talCreateUserMessage("existing")},
			},
			StreamFn: func(_ context.Context, _ *ai.Model, tr *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
				var users []string
				for _, m := range tr.Messages {
					if u, ok := m.(*ai.UserMessage); ok && len(u.Content.Blocks) > 0 {
						users = append(users, u.Content.Blocks[0].(*ai.TextContent).Text)
					}
				}
				usersByReq = append(usersByReq, users)
				return talPushAssistantDone(talCreateAssistantMessage(ai.ContentList{&ai.TextContent{Type: "text", Text: "ok"}}, ai.StopReasonStop)), nil
			},
		})
		ag3.Steer(talCreateUserMessage("steering"))
		ag3.FollowUp(talCreateUserMessage("follow-up"))
		if err := ag3.Continue(t.Context()); err != nil {
			t.Fatalf("Continue steering+followUp: %v", err)
		}
		if len(usersByReq) != 2 || strings.Contains(strings.Join(usersByReq[0], ","), "follow-up") || !strings.Contains(strings.Join(usersByReq[1], ","), "follow-up") {
			t.Fatalf("steering must precede follow-up: %v", usersByReq)
		}
	})

	t.Run("keeps queues on error/aborted and FinishTurn end, and PeekQueuedMessages previews cleanly", func(t *testing.T) {
		for _, reason := range []ai.StopReason{ai.StopReasonError, ai.StopReasonAborted} {
			steer := talCreateUserMessage("steering")
			follow := talCreateUserMessage("follow-up")
			var ag *agent.Agent
			ag = agent.NewAgent(&agent.AgentOptions{
				InitialState: &agent.AgentInitialState{Model: tagentMockModel()},
				FinishTurn: func(_ context.Context, _ *agent.AgentTurnContext) (*agent.AgentTurnDecision, error) {
					return &agent.AgentTurnDecision{Action: agent.AgentTurnDecisionContinue}, nil
				},
				StreamFn: func(_ context.Context, _ *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
					return talPushAssistantError(talCreateAssistantMessage(ai.ContentList{}, reason, string(reason)), reason), nil
				},
			})
			ag.FollowUp(follow)
			ag.Subscribe(func(ev agent.AgentEvent) {
				if e, ok := ev.(*agent.AgentEventMessageEnd); ok && e.Message.GetRole() == "assistant" {
					ag.Steer(steer)
				}
			})
			if err := ag.PromptText(t.Context(), "start"); err != nil {
				t.Fatalf("%s PromptText: %v", reason, err)
			}
			if peek := ag.PeekQueuedMessages(); len(peek.Steering) != 1 || peek.Steering[0] != steer || len(peek.FollowUp) != 1 || peek.FollowUp[0] != follow {
				t.Fatalf("%s expected peek steering=[steer] follow=[follow], got %+v", reason, peek)
			}
			ag.ClearSteeringQueue()
			if peek := ag.PeekQueuedMessages(); len(peek.Steering) != 0 || len(peek.FollowUp) != 1 || peek.FollowUp[0] != follow {
				t.Fatalf("%s expected peek follow=[follow], got %+v", reason, peek)
			}
		}

		// FinishTurn Action:End keeps queues.
		steer := talCreateUserMessage("steering")
		follow := talCreateUserMessage("follow-up")
		var agEnd *agent.Agent
		agEnd = agent.NewAgent(&agent.AgentOptions{
			InitialState: &agent.AgentInitialState{Model: tagentMockModel()},
			FinishTurn: func(_ context.Context, _ *agent.AgentTurnContext) (*agent.AgentTurnDecision, error) {
				return &agent.AgentTurnDecision{Action: agent.AgentTurnDecisionEnd}, nil
			},
			StreamFn: func(_ context.Context, _ *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
				return talPushAssistantDone(talCreateAssistantMessage(ai.ContentList{&ai.TextContent{Type: "text", Text: "done"}}, ai.StopReasonStop)), nil
			},
		})
		agEnd.FollowUp(follow)
		agEnd.Subscribe(func(ev agent.AgentEvent) {
			if e, ok := ev.(*agent.AgentEventMessageEnd); ok && e.Message.GetRole() == "assistant" {
				agEnd.Steer(steer)
			}
		})
		if err := agEnd.PromptText(t.Context(), "start"); err != nil {
			t.Fatalf("PromptText end: %v", err)
		}
		if peek := agEnd.PeekQueuedMessages(); len(peek.Steering) != 1 || peek.Steering[0] != steer || len(peek.FollowUp) != 1 || peek.FollowUp[0] != follow {
			t.Fatalf("FinishTurn end expected peek steering=[steer] follow=[follow], got %+v", peek)
		}
		agEnd.ClearSteeringQueue()
		if peek := agEnd.PeekQueuedMessages(); len(peek.Steering) != 0 || len(peek.FollowUp) != 1 || peek.FollowUp[0] != follow {
			t.Fatalf("FinishTurn end expected peek follow=[follow], got %+v", peek)
		}

		// PeekQueuedMessages previews queued items without draining.
		agPeek := agent.NewAgent(&agent.AgentOptions{
			InitialState: &agent.AgentInitialState{Model: tagentMockModel()},
			SteeringMode: agent.QueueModeOneAtATime,
			FollowUpMode: agent.QueueModeAll,
		})
		s1 := talCreateUserMessage("s1")
		s2 := talCreateUserMessage("s2")
		f1 := talCreateUserMessage("f1")
		agPeek.Steer(s1)
		agPeek.Steer(s2)
		agPeek.FollowUp(f1)
		if p := agPeek.PeekQueuedMessages(); len(p.Steering) != 2 || p.Steering[0] != s1 || p.Steering[1] != s2 || len(p.FollowUp) != 1 || p.FollowUp[0] != f1 {
			t.Fatalf("expected [s1 s2] and [f1] first peek, got %+v", p)
		}
		if p := agPeek.PeekQueuedMessages(); len(p.Steering) != 2 || len(p.FollowUp) != 1 {
			t.Fatalf("expected non-consuming peek, got %+v", p)
		}
		agPeek.ClearSteeringQueue()
		if p := agPeek.PeekQueuedMessages(); len(p.Steering) != 0 || len(p.FollowUp) != 1 || p.FollowUp[0] != f1 {
			t.Fatalf("expected [f1] after clearing steering, got %+v", p)
		}
	})

	t.Run("legacy PrepareNextTurn ctx-only callback and Identity merging", func(t *testing.T) {
		noop := &agent.AgentTool{
			Name:        "noop",
			Label:       "Noop",
			Description: "Noop",
			Parameters:  talSimpleSchema(),
			Execute: func(_ context.Context, _ string, _ any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
				return agent.AgentToolResult{Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "ok"}}}, nil
			},
		}
		reqCount := 0
		sawActiveCtx := false
		var seenIdentities []*ai.ClientIdentity

		ag := agent.NewAgent(&agent.AgentOptions{
			InitialState: &agent.AgentInitialState{
				Model: tagentMockModel(),
				Tools: []*agent.AgentTool{noop},
			},
			PrepareNextTurn: func(ctx context.Context) (*agent.AgentLoopTurnUpdate, error) {
				sawActiveCtx = ctx != nil && ctx.Err() == nil
				return nil, nil
			},
			StreamFn: func(_ context.Context, _ *ai.Model, _ *ai.TranscriptContext, opts *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
				reqCount++
				seenIdentities = append(seenIdentities, opts.Identity)
				if reqCount == 1 {
					return talPushAssistantDone(talCreateAssistantMessage(ai.ContentList{
						&ai.ToolCall{Type: "toolCall", Id: "t1", Name: "noop", Arguments: jsonx.NewObject()},
					}, ai.StopReasonToolUse)), nil
				}
				return talPushAssistantDone(talCreateAssistantMessage(ai.ContentList{&ai.TextContent{Type: "text", Text: "done"}}, ai.StopReasonStop)), nil
			},
		})
		if err := ag.PromptText(t.Context(), "start"); err != nil {
			t.Fatalf("PromptText 1: %v", err)
		}
		if reqCount != 2 || !sawActiveCtx {
			t.Fatalf("expected legacy PrepareNextTurn called with active ctx, reqCount=%d sawActiveCtx=%v", reqCount, sawActiveCtx)
		}

		// Check default pi attribution & configured Identity merge.
		ag.SetIdentity(&ai.ClientIdentity{
			UserAgent:   ai.UserAgentModeValue{Val: ai.UserAgentModeShort},
			Attribution: jsonx.Some(ai.ClientAttribution{Name: "acme", Url: "https://acme.example"}),
		})
		if err := ag.PromptText(t.Context(), "second"); err != nil {
			t.Fatalf("PromptText 2: %v", err)
		}
		ag.SetIdentity(&ai.ClientIdentity{UserAgent: ai.UserAgentModeValue{Val: ai.UserAgentModeShort}})
		if err := ag.PromptText(t.Context(), "third"); err != nil {
			t.Fatalf("PromptText 3: %v", err)
		}

		attr0, ok0 := seenIdentities[0].Attribution.Get()
		if !ok0 || attr0.Name != ai.PiAttribution.Name {
			t.Fatalf("expected default PiAttribution, got %+v", seenIdentities[0])
		}
		attr2, ok2 := seenIdentities[2].Attribution.Get()
		if !ok2 || attr2.Name != "acme" || seenIdentities[2].UserAgent.Val != ai.UserAgentModeShort {
			t.Fatalf("expected custom acme attribution + UserAgent, got %+v", seenIdentities[2])
		}
		attr3, ok3 := seenIdentities[3].Attribution.Get()
		if !ok3 || attr3.Name != ai.PiAttribution.Name || seenIdentities[3].UserAgent.Val != ai.UserAgentModeShort {
			t.Fatalf("expected default PiAttribution merged with custom UserAgent, got %+v", seenIdentities[3])
		}
	})
}

func TestAgent_HookGettersAndSetters(t *testing.T) {
	ag := agent.NewAgent(&agent.AgentOptions{
		InitialState: &agent.AgentInitialState{Model: tagentMockModel()},
	})

	// Default ConvertToLlm is non-nil and behaves like DefaultConvertToLlm.
	defConv := ag.ConvertToLlm()
	if defConv == nil {
		t.Fatal("expected non-nil default ConvertToLlm")
	}
	userMsg := talCreateUserMessage("hi")
	converted, err := defConv(t.Context(), []agent.AgentMessage{userMsg})
	if err != nil || len(converted) != 1 {
		t.Fatalf("expected default ConvertToLlm to convert user message, len=%d err=%v", len(converted), err)
	}

	if ag.TransformContext() != nil || ag.GetApiKey() != nil || ag.BeforeToolCall() != nil ||
		ag.AfterToolCall() != nil || ag.FinishTurn() != nil || ag.PrepareRequest() != nil ||
		ag.PrepareNextTurn() != nil || ag.PrepareNextTurnWithContext() != nil ||
		ag.OnPayload() != nil || ag.OnResponse() != nil || ag.OnProviderStreamEvent() != nil ||
		ag.HTTPClient() != nil {
		t.Fatal("expected optional hooks to default to nil")
	}

	var callsMu sync.Mutex
	var calls struct {
		beforeTool       int
		afterTool        int
		prepareRequest   int
		finishTurn       int
		prepareNextCtx   int
		transformContext int
		convertToLlm     int
		getApiKey        int
		sawOptsHTTP      bool
		sawOptsPayload   bool
		sawOptsResp      bool
		sawOptsStreamEv  bool
	}

	customClient := &http.Client{Timeout: 5 * time.Second}
	onPayload := func(payload any, _ *ai.Model) (any, error) { return payload, nil }
	onResponse := func(_ *ai.ProviderResponse, _ *ai.Model) error { return nil }
	onStreamEv := func(_ any, _ *ai.Model) error { return nil }

	ag.SetHTTPClient(customClient)
	ag.SetOnPayload(onPayload)
	ag.SetOnResponse(onResponse)
	ag.SetOnProviderStreamEvent(onStreamEv)
	ag.SetGetApiKey(func(_ context.Context, provider string) (string, error) {
		callsMu.Lock()
		calls.getApiKey++
		callsMu.Unlock()
		return "key-" + provider, nil
	})
	ag.SetTransformContext(func(_ context.Context, msgs []agent.AgentMessage) ([]agent.AgentMessage, error) {
		callsMu.Lock()
		calls.transformContext++
		callsMu.Unlock()
		return msgs, nil
	})
	ag.SetConvertToLlm(func(_ context.Context, msgs []agent.AgentMessage) ([]ai.Message, error) {
		callsMu.Lock()
		calls.convertToLlm++
		callsMu.Unlock()
		return agent.DefaultConvertToLlm(msgs), nil
	})
	ag.SetPrepareRequest(func(_ context.Context, _ *agent.PrepareRequestContext) (*agent.AgentRequestUpdate, error) {
		callsMu.Lock()
		calls.prepareRequest++
		callsMu.Unlock()
		return nil, nil
	})
	ag.SetBeforeToolCall(func(_ context.Context, _ *agent.BeforeToolCallContext) (*agent.BeforeToolCallResult, error) {
		callsMu.Lock()
		calls.beforeTool++
		callsMu.Unlock()
		return nil, nil
	})
	ag.SetAfterToolCall(func(_ context.Context, _ *agent.AfterToolCallContext) (*agent.AfterToolCallResult, error) {
		callsMu.Lock()
		calls.afterTool++
		callsMu.Unlock()
		return nil, nil
	})
	ag.SetFinishTurn(func(_ context.Context, _ *agent.AgentTurnContext) (*agent.AgentTurnDecision, error) {
		callsMu.Lock()
		calls.finishTurn++
		callsMu.Unlock()
		return nil, nil
	})
	ag.SetPrepareNextTurn(func(_ context.Context) (*agent.AgentLoopTurnUpdate, error) { return nil, nil })
	ag.SetPrepareNextTurnWithContext(func(_ context.Context, _ *agent.PrepareNextTurnContext) (*agent.AgentLoopTurnUpdate, error) {
		callsMu.Lock()
		calls.prepareNextCtx++
		callsMu.Unlock()
		return nil, nil
	})

	if ag.HTTPClient() != customClient || ag.OnPayload() == nil || ag.OnResponse() == nil ||
		ag.OnProviderStreamEvent() == nil || ag.GetApiKey() == nil || ag.TransformContext() == nil ||
		ag.ConvertToLlm() == nil || ag.PrepareRequest() == nil || ag.BeforeToolCall() == nil ||
		ag.AfterToolCall() == nil || ag.FinishTurn() == nil || ag.PrepareNextTurn() == nil ||
		ag.PrepareNextTurnWithContext() == nil {
		t.Fatal("expected all hook getters to reflect updated values")
	}

	noopTool := &agent.AgentTool{
		Name:        "ping",
		Label:       "Ping",
		Description: "Ping",
		Parameters:  talSimpleSchema(),
		Execute: func(_ context.Context, _ string, _ any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
			return agent.AgentToolResult{Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "pong"}}}, nil
		},
	}
	ag.SetTools([]*agent.AgentTool{noopTool})

	var turn atomic.Int32
	ag.SetStreamFn(func(_ context.Context, _ *ai.Model, _ *ai.TranscriptContext, opts *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
		callsMu.Lock()
		calls.sawOptsHTTP = opts.HTTPClient == customClient
		calls.sawOptsPayload = opts.OnPayload != nil
		calls.sawOptsResp = opts.OnResponse != nil
		calls.sawOptsStreamEv = opts.OnProviderStreamEvent != nil
		callsMu.Unlock()

		if turn.Add(1) == 1 {
			return talPushAssistantDone(talCreateAssistantMessage(ai.ContentList{
				&ai.ToolCall{Type: "toolCall", Id: "call-1", Name: "ping", Arguments: jsonx.NewObject()},
			}, ai.StopReasonToolUse)), nil
		}
		return talPushAssistantDone(talCreateAssistantMessage(ai.ContentList{&ai.TextContent{Type: "text", Text: "done"}}, ai.StopReasonStop)), nil
	})

	if err := ag.PromptText(t.Context(), "go"); err != nil {
		t.Fatalf("PromptText failed: %v", err)
	}

	callsMu.Lock()
	if calls.beforeTool != 1 || calls.afterTool != 1 || calls.prepareRequest != 2 ||
		calls.finishTurn != 2 || calls.prepareNextCtx != 1 || calls.transformContext != 2 ||
		calls.convertToLlm != 2 || calls.getApiKey != 2 ||
		!calls.sawOptsHTTP || !calls.sawOptsPayload || !calls.sawOptsResp || !calls.sawOptsStreamEv {
		got := calls
		callsMu.Unlock()
		t.Fatalf("unexpected hook invocations after setter updates: %+v", got)
	}
	callsMu.Unlock()

	// SetConvertToLlm(nil) restores default ConvertToLlm behavior cleanly.
	ag.SetConvertToLlm(nil)
	if ag.ConvertToLlm() == nil {
		t.Fatal("expected ConvertToLlm() to remain non-nil after SetConvertToLlm(nil)")
	}
	if _, err := ag.ConvertToLlm()(t.Context(), []agent.AgentMessage{userMsg}); err != nil {
		t.Fatalf("default ConvertToLlm call failed after reset: %v", err)
	}
	ag.SetTools(nil)
	if err := ag.PromptText(t.Context(), "after reset"); err != nil {
		t.Fatalf("PromptText after SetConvertToLlm(nil) failed: %v", err)
	}
	callsMu.Lock()
	if calls.convertToLlm != 2 {
		gotConv := calls.convertToLlm
		callsMu.Unlock()
		t.Fatalf("expected custom convertToLlm not invoked after SetConvertToLlm(nil), got %d", gotConv)
	}
	callsMu.Unlock()

	// Concurrent get/set race test across all newly added hooks.
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for range 50 {
				ag.SetBeforeToolCall(func(_ context.Context, _ *agent.BeforeToolCallContext) (*agent.BeforeToolCallResult, error) {
					return nil, nil
				})
				_ = ag.BeforeToolCall()
				ag.SetAfterToolCall(func(_ context.Context, _ *agent.AfterToolCallContext) (*agent.AfterToolCallResult, error) {
					return nil, nil
				})
				_ = ag.AfterToolCall()
				ag.SetPrepareRequest(func(_ context.Context, _ *agent.PrepareRequestContext) (*agent.AgentRequestUpdate, error) {
					return nil, nil
				})
				_ = ag.PrepareRequest()
				ag.SetFinishTurn(func(_ context.Context, _ *agent.AgentTurnContext) (*agent.AgentTurnDecision, error) {
					return nil, nil
				})
				_ = ag.FinishTurn()
				ag.SetPrepareNextTurn(func(_ context.Context) (*agent.AgentLoopTurnUpdate, error) { return nil, nil })
				_ = ag.PrepareNextTurn()
				ag.SetPrepareNextTurnWithContext(func(_ context.Context, _ *agent.PrepareNextTurnContext) (*agent.AgentLoopTurnUpdate, error) {
					return nil, nil
				})
				_ = ag.PrepareNextTurnWithContext()
				ag.SetTransformContext(func(_ context.Context, msgs []agent.AgentMessage) ([]agent.AgentMessage, error) {
					return msgs, nil
				})
				_ = ag.TransformContext()
				ag.SetGetApiKey(func(_ context.Context, _ string) (string, error) { return "", nil })
				_ = ag.GetApiKey()
				if id%2 == 0 {
					ag.SetConvertToLlm(nil)
				} else {
					ag.SetConvertToLlm(func(_ context.Context, msgs []agent.AgentMessage) ([]ai.Message, error) {
						return agent.DefaultConvertToLlm(msgs), nil
					})
				}
				_ = ag.ConvertToLlm()
				ag.SetOnPayload(onPayload)
				_ = ag.OnPayload()
				ag.SetOnResponse(onResponse)
				_ = ag.OnResponse()
				ag.SetOnProviderStreamEvent(onStreamEv)
				_ = ag.OnProviderStreamEvent()
				ag.SetHTTPClient(customClient)
				_ = ag.HTTPClient()
			}
		}(g)
	}
	wg.Wait()
}
