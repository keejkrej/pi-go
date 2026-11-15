// Ported from packages/agent/test/e2e.test.ts (pi v1.0.0) - faux provider suite.

package agent_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/agent/internal/testkit"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func te2eFauxModel(reasoning bool) *ai.Model {
	return &ai.Model{
		BaseModel: ai.BaseModel{
			Id:       "faux-1",
			Name:     "Faux Test Model",
			Api:      string(ai.KnownApiAnthropicMessages),
			Provider: ai.KnownProviderAnthropic,
			BaseUrl:  "https://faux.invalid",
			Input:    []ai.ModelModality{ai.ModelModalityText},
		},
		Reasoning:     reasoning,
		ContextWindow: 128000,
		MaxTokens:     4096,
	}
}

func TestE2E_BasicPrompt(t *testing.T) {
	fauxStream := func(_ context.Context, m *ai.Model, transcript *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
		if ai.GetCurrentSystemPrompt(transcript.Messages) != "You are a helpful assistant." {
			t.Fatalf("unexpected system prompt: %q", ai.GetCurrentSystemPrompt(transcript.Messages))
		}
		final := &ai.AssistantMessage{
			Role: "assistant",
			Content: ai.ContentList{
				&ai.TextContent{Type: "text", Text: "Hello! How can I help you?"},
			},
			Api:        ai.Api(m.Api),
			Provider:   m.Provider,
			Model:      m.Id,
			StopReason: ai.StopReasonStop,
			Timestamp:  time.Now().UnixMilli(),
		}
		s := ai.NewAssistantMessageEventStream()
		partial := &ai.AssistantMessage{
			Role:       "assistant",
			Content:    ai.ContentList{},
			Api:        ai.Api(m.Api),
			Provider:   m.Provider,
			Model:      m.Id,
			StopReason: ai.StopReasonStop,
			Timestamp:  final.Timestamp,
		}
		s.Push(&ai.AssistantMessageEventStart{Type: "start", Partial: partial})
		s.Push(&ai.AssistantMessageEventDone{Type: "done", Reason: ai.StopReasonStop, Message: final})
		s.End(final)
		return s, nil
	}

	ag := agent.NewAgent(&agent.AgentOptions{
		InitialState: &agent.AgentInitialState{
			SystemPrompt: "You are a helpful assistant.",
			Model:        te2eFauxModel(false),
		},
		StreamFn: fauxStream,
	})

	if err := ag.PromptText(t.Context(), "Hello!"); err != nil {
		t.Fatalf("PromptText: %v", err)
	}
	st := ag.State()
	if st.IsStreaming || st.ErrorMessage != nil {
		t.Fatalf("expected idle clean state, got streaming=%v err=%v", st.IsStreaming, st.ErrorMessage)
	}
	// [SystemMessage, UserMessage, AssistantMessage]
	if len(st.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(st.Messages))
	}
	last, ok := st.Messages[2].(*ai.AssistantMessage)
	if !ok || last.Content[0].(*ai.TextContent).Text != "Hello! How can I help you?" {
		t.Fatalf("unexpected assistant response: %#v", st.Messages[2])
	}
}

func TestE2E_ToolExecutionWithCalculateTool(t *testing.T) {
	turn := 0
	fauxStream := func(_ context.Context, m *ai.Model, transcript *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
		turn++
		if turn == 1 {
			return talPushAssistantDone(&ai.AssistantMessage{
				Role: "assistant",
				Content: ai.ContentList{
					&ai.ToolCall{
						Type:      "toolCall",
						Id:        "calc-1",
						Name:      "calculate",
						Arguments: jsonx.ObjectOf("expression", "123 * 456"),
					},
				},
				Api:        ai.Api(m.Api),
				Provider:   m.Provider,
				Model:      m.Id,
				StopReason: ai.StopReasonToolUse,
				Timestamp:  time.Now().UnixMilli(),
			}), nil
		}
		// Verify toolResult reached turn 2 transcript.
		var foundToolOutput string
		for _, msg := range transcript.Messages {
			if tr, ok := msg.(*ai.ToolResultMessage); ok {
				foundToolOutput = tr.Content[0].(*ai.TextContent).Text
			}
		}
		if !strings.Contains(foundToolOutput, "56088") {
			t.Fatalf("expected turn 2 transcript to carry 56088, got %q", foundToolOutput)
		}
		return talPushAssistantDone(&ai.AssistantMessage{
			Role: "assistant",
			Content: ai.ContentList{
				&ai.TextContent{Type: "text", Text: "123 * 456 equals 56088."},
			},
			Api:        ai.Api(m.Api),
			Provider:   m.Provider,
			Model:      m.Id,
			StopReason: ai.StopReasonStop,
			Timestamp:  time.Now().UnixMilli(),
		}), nil
	}

	ag := agent.NewAgent(&agent.AgentOptions{
		InitialState: &agent.AgentInitialState{
			SystemPrompt: "Use tools.",
			Model:        te2eFauxModel(false),
			Tools:        []*agent.AgentTool{testkit.CalculateTool()},
		},
		StreamFn: fauxStream,
	})

	if err := ag.PromptText(t.Context(), "What is 123 * 456?"); err != nil {
		t.Fatalf("PromptText: %v", err)
	}
	st := ag.State()
	// [System, User, Assistant(toolCall), ToolResult, Assistant(final)]
	if len(st.Messages) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(st.Messages))
	}
	tr, ok := st.Messages[3].(*ai.ToolResultMessage)
	if !ok || tr.IsError {
		t.Fatalf("expected successful ToolResultMessage, got %#v", st.Messages[3])
	}
	det, _ := tr.Details.Get()
	calcRes, ok := det.(testkit.CalculateResult)
	if !ok || calcRes.Result != 56088 {
		t.Fatalf("expected CalculateResult 56088, got %#v", det)
	}
}

func TestE2E_AbortExecutionAndStateUpdates(t *testing.T) {
	started := make(chan struct{})
	fauxStream := func(ctx context.Context, m *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
		s := ai.NewAssistantMessageEventStream()
		go func() {
			partial := &ai.AssistantMessage{
				Role:       "assistant",
				Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: "partial"}},
				Api:        ai.Api(m.Api),
				Provider:   m.Provider,
				Model:      m.Id,
				StopReason: ai.StopReasonStop,
				Timestamp:  time.Now().UnixMilli(),
			}
			s.Push(&ai.AssistantMessageEventStart{Type: "start", Partial: partial})
			close(started)
			<-ctx.Done()
			errTxt := "Aborted by user"
			aborted := &ai.AssistantMessage{
				Role:         "assistant",
				Content:      ai.ContentList{&ai.TextContent{Type: "text", Text: "partial"}},
				Api:          ai.Api(m.Api),
				Provider:     m.Provider,
				Model:        m.Id,
				StopReason:   ai.StopReasonAborted,
				ErrorMessage: &errTxt,
				Timestamp:    partial.Timestamp,
			}
			s.Push(&ai.AssistantMessageEventError{Type: "error", Reason: ai.StopReasonAborted, Error: aborted})
			s.End(aborted)
		}()
		return s, nil
	}

	ag := agent.NewAgent(&agent.AgentOptions{
		InitialState: &agent.AgentInitialState{
			Model: te2eFauxModel(false),
		},
		StreamFn: fauxStream,
	})

	var sawStreamingTrue bool
	unsub := ag.Subscribe(func(ev agent.AgentEvent) {
		if _, ok := ev.(*agent.AgentEventMessageStart); ok {
			if ag.IsStreaming() && ag.StreamingMessage() != nil {
				sawStreamingTrue = true
			}
		}
	})
	defer unsub()

	doneErr := make(chan error, 1)
	go func() {
		doneErr <- ag.PromptText(t.Context(), "Cancel me")
	}()

	<-started
	ag.Abort()
	if err := ag.WaitForIdle(t.Context()); err != nil {
		t.Fatalf("WaitForIdle: %v", err)
	}
	if err := <-doneErr; err != nil {
		t.Fatalf("PromptText returned err: %v", err)
	}

	if !sawStreamingTrue {
		t.Fatalf("expected subscriber to observe IsStreaming and StreamingMessage during stream")
	}
	st := ag.State()
	if st.IsStreaming {
		t.Fatalf("expected IsStreaming false after abort")
	}
	last := st.Messages[len(st.Messages)-1].(*ai.AssistantMessage)
	if last.StopReason != ai.StopReasonAborted {
		t.Fatalf("expected StopReasonAborted, got %q", last.StopReason)
	}
}

func TestE2E_MultiTurnThinkingAndContinueScenarios(t *testing.T) {
	t.Run("multi-turn conversation preserves history", func(t *testing.T) {
		turn := 0
		fauxStream := func(_ context.Context, m *ai.Model, transcript *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
			turn++
			reply := "Nice to meet you, Alice."
			if turn == 2 {
				var sawAlice bool
				for _, msg := range transcript.Messages {
					if u, ok := msg.(*ai.UserMessage); ok && len(u.Content.Blocks) > 0 {
						if strings.Contains(u.Content.Blocks[0].(*ai.TextContent).Text, "Alice") {
							sawAlice = true
						}
					}
				}
				if !sawAlice {
					t.Fatalf("expected turn 2 context to retain turn 1 user message")
				}
				reply = "Your name is Alice."
			}
			return talPushAssistantDone(&ai.AssistantMessage{
				Role:       "assistant",
				Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: reply}},
				Api:        ai.Api(m.Api),
				Provider:   m.Provider,
				Model:      m.Id,
				StopReason: ai.StopReasonStop,
				Timestamp:  time.Now().UnixMilli(),
			}), nil
		}

		ag := agent.NewAgent(&agent.AgentOptions{
			InitialState: &agent.AgentInitialState{Model: te2eFauxModel(false)},
			StreamFn:     fauxStream,
		})
		if err := ag.PromptText(t.Context(), "My name is Alice."); err != nil {
			t.Fatalf("turn 1: %v", err)
		}
		if err := ag.PromptText(t.Context(), "What is my name?"); err != nil {
			t.Fatalf("turn 2: %v", err)
		}
		if len(ag.Messages()) != 4 {
			t.Fatalf("expected 4 messages after 2 turns, got %d", len(ag.Messages()))
		}
	})

	t.Run("thinking blocks preserved with Reasoning level", func(t *testing.T) {
		var seenReasoning *ai.ThinkingLevel
		fauxStream := func(_ context.Context, m *ai.Model, _ *ai.TranscriptContext, opts *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
			seenReasoning = opts.Reasoning
			return talPushAssistantDone(&ai.AssistantMessage{
				Role: "assistant",
				Content: ai.ContentList{
					&ai.ThinkingContent{Type: "thinking", Thinking: "Analyzing step by step"},
					&ai.TextContent{Type: "text", Text: "42"},
				},
				Api:        ai.Api(m.Api),
				Provider:   m.Provider,
				Model:      m.Id,
				StopReason: ai.StopReasonStop,
				Timestamp:  time.Now().UnixMilli(),
			}), nil
		}

		ag := agent.NewAgent(&agent.AgentOptions{
			InitialState: &agent.AgentInitialState{
				Model:         te2eFauxModel(true),
				ThinkingLevel: agent.ThinkingLevelHigh,
			},
			StreamFn: fauxStream,
		})
		if err := ag.PromptText(t.Context(), "Answer?"); err != nil {
			t.Fatalf("PromptText: %v", err)
		}
		if seenReasoning == nil || *seenReasoning != ai.ThinkingLevelHigh {
			t.Fatalf("expected Reasoning high forwarded, got %v", seenReasoning)
		}
		last := ag.Messages()[len(ag.Messages())-1].(*ai.AssistantMessage)
		if _, ok := last.Content[0].(*ai.ThinkingContent); !ok {
			t.Fatalf("expected ThinkingContent first block, got %T", last.Content[0])
		}
	})

	t.Run("Continue() validation and continuation from user and toolResult", func(t *testing.T) {
		ag := agent.NewAgent(&agent.AgentOptions{
			InitialState: &agent.AgentInitialState{Model: te2eFauxModel(false)},
			StreamFn: func(_ context.Context, m *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
				return talPushAssistantDone(&ai.AssistantMessage{
					Role:       "assistant",
					Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: "continued reply"}},
					Api:        ai.Api(m.Api),
					Provider:   m.Provider,
					Model:      m.Id,
					StopReason: ai.StopReasonStop,
					Timestamp:  time.Now().UnixMilli(),
				}), nil
			},
		})

		if err := ag.Continue(t.Context()); err == nil || !strings.Contains(err.Error(), "No messages") {
			t.Fatalf("expected empty Continue error, got %v", err)
		}

		userMsg := talCreateUserMessage("start")
		ag.ReplaceMessages([]agent.AgentMessage{userMsg})
		if err := ag.Continue(t.Context()); err != nil {
			t.Fatalf("Continue from user message: %v", err)
		}
		if len(ag.Messages()) != 2 {
			t.Fatalf("expected 2 messages after continuing from user, got %d", len(ag.Messages()))
		}

		// Trailing assistant without queued messages must error.
		if err := ag.Continue(t.Context()); err == nil || !strings.Contains(err.Error(), "assistant") {
			t.Fatalf("expected assistant Continue error, got %v", err)
		}

		// Continue from trailing toolResult message.
		tr := &ai.ToolResultMessage{
			Role:       "toolResult",
			ToolCallId: "tc-1",
			ToolName:   "calculate",
			Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: "4"}},
			Timestamp:  time.Now().UnixMilli(),
		}
		ag.AppendMessage(tr)
		if err := ag.Continue(t.Context()); err != nil {
			t.Fatalf("Continue from toolResult: %v", err)
		}
		if len(ag.Messages()) != 4 {
			t.Fatalf("expected 4 messages after continuing from toolResult, got %d", len(ag.Messages()))
		}
	})
}
