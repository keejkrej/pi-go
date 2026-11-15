// Ported from packages/coding-agent/test/suite/agent-session-retry-events.test.ts (pi v1.5.2).

package core_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	core "github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/internal/testkit"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/typebox"
)

func tsasreBoolPtr(v bool) *bool { return &v }

func tsasreIntPtr(v int) *int { return &v }

func tsasreFloatPtr(v float64) *float64 { return &v }

func tsasreStrPtr(v string) *string { return &v }

func tsasreFastRetrySettings(maxRetries int, enabled bool) *core.Settings {
	return &core.Settings{
		Retry: &core.RetrySettings{
			Enabled:         tsasreBoolPtr(enabled),
			MaxRetries:      tsasreIntPtr(maxRetries),
			BaseDelayMs:     tsasreFloatPtr(5),
			MaxAgentDelayMs: tsasreFloatPtr(20),
		},
	}
}

func tsasreErrorAssistantMessage(errText string) *ai.AssistantMessage {
	stopErr := ai.StopReasonError
	return providers.FauxAssistantMessage(
		providers.FauxAssistantText(""),
		&providers.FauxAssistantMessageOptions{
			StopReason:   &stopErr,
			ErrorMessage: tsasreStrPtr(errText),
		},
	)
}

func TestAgentSessionRetryEventsCharacterization(t *testing.T) {
	ctx := context.Background()

	t.Run("retries a transient error sets willRetry on agent_end and succeeds", func(t *testing.T) {
		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Settings: tsasreFastRetrySettings(3, true),
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{Message: tsasreErrorAssistantMessage("429 Too Many Requests: rate_limit_exceeded")},
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("recovered"), nil)},
		})

		if err := h.Session.Prompt(ctx, "hello"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}

		agentEnds := h.EventsOfType("agent_end")
		if len(agentEnds) != 2 {
			t.Fatalf("expected 2 agent_end events, got %d", len(agentEnds))
		}
		ae1 := agentEnds[0].(*core.AgentSessionEventAgentEnd)
		ae2 := agentEnds[1].(*core.AgentSessionEventAgentEnd)
		if !ae1.WillRetry {
			t.Fatal("expected first agent_end.WillRetry == true")
		}
		if ae2.WillRetry {
			t.Fatal("expected final agent_end.WillRetry == false")
		}

		retryStarts := h.EventsOfType("auto_retry_start")
		retryEnds := h.EventsOfType("auto_retry_end")
		if len(retryStarts) != 1 || len(retryEnds) != 1 {
			t.Fatalf("expected 1 auto_retry_start and 1 auto_retry_end, got %d / %d", len(retryStarts), len(retryEnds))
		}
		re := retryEnds[0].(*core.AgentSessionEventAutoRetryEnd)
		if !re.Success || re.Attempt != 1 {
			t.Fatalf("expected auto_retry_end {Success:true, Attempt:1}, got %+v", re)
		}
		texts := testkit.GetAssistantTexts(h)
		if len(texts) == 0 || texts[len(texts)-1] != "recovered" {
			t.Fatalf("expected final assistant text 'recovered', got %#v", texts)
		}
	})

	t.Run("retries multiple transient errors until success", func(t *testing.T) {
		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Settings: tsasreFastRetrySettings(3, true),
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{Message: tsasreErrorAssistantMessage("503 Service Unavailable")},
			providers.FauxResponseMessage{Message: tsasreErrorAssistantMessage("overloaded_error: try again")},
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("finally ok"), nil)},
		})

		if err := h.Session.Prompt(ctx, "hello"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}

		retryStarts := h.EventsOfType("auto_retry_start")
		retryEnds := h.EventsOfType("auto_retry_end")
		if len(retryStarts) != 2 || len(retryEnds) != 1 {
			t.Fatalf("expected 2 auto_retry_start and 1 auto_retry_end, got %d / %d", len(retryStarts), len(retryEnds))
		}
		if retryStarts[0].(*core.AgentSessionEventAutoRetryStart).Attempt != 1 || retryStarts[1].(*core.AgentSessionEventAutoRetryStart).Attempt != 2 {
			t.Fatalf("unexpected attempts in auto_retry_start")
		}
		re := retryEnds[0].(*core.AgentSessionEventAutoRetryEnd)
		if !re.Success || re.Attempt != 2 {
			t.Fatalf("expected auto_retry_end {Success:true, Attempt:2}, got %+v", re)
		}
	})

	t.Run("stops retrying and resets attempt when aborted during retry flow", func(t *testing.T) {
		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Settings: &core.Settings{
				Retry: &core.RetrySettings{
					Enabled:         tsasreBoolPtr(true),
					MaxRetries:      tsasreIntPtr(3),
					BaseDelayMs:     tsasreFloatPtr(5000),
					MaxAgentDelayMs: tsasreFloatPtr(5000),
				},
			},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		retryStarted := make(chan struct{})
		var once sync.Once
		unsub := h.Session.Subscribe(func(ev core.AgentSessionEvent) {
			if _, ok := ev.(*core.AgentSessionEventAutoRetryStart); ok {
				once.Do(func() { close(retryStarted) })
			}
		})
		defer unsub()

		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{Message: tsasreErrorAssistantMessage("429 Too Many Requests")},
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("should not reach"), nil)},
		})

		errCh := make(chan error, 1)
		go func() {
			errCh <- h.Session.Prompt(ctx, "hello")
		}()

		select {
		case <-retryStarted:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for auto_retry_start")
		}

		if err := h.Session.Abort(ctx); err != nil {
			t.Fatalf("Abort: %v", err)
		}
		<-errCh

		if h.Session.RetryAttempt() != 0 || h.Session.IsRetrying() {
			t.Fatalf("expected RetryAttempt==0 and IsRetrying==false after abort, got attempt=%d retrying=%v", h.Session.RetryAttempt(), h.Session.IsRetrying())
		}
		retryEnds := h.EventsOfType("auto_retry_end")
		if len(retryEnds) != 1 || retryEnds[0].(*core.AgentSessionEventAutoRetryEnd).Success {
			t.Fatalf("expected unsuccessful auto_retry_end after abort, got %#v", retryEnds)
		}
	})

	t.Run("stops after exhausting maxRetries", func(t *testing.T) {
		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Settings: tsasreFastRetrySettings(2, true),
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{Message: tsasreErrorAssistantMessage("429 Too Many Requests #1")},
			providers.FauxResponseMessage{Message: tsasreErrorAssistantMessage("429 Too Many Requests #2")},
			providers.FauxResponseMessage{Message: tsasreErrorAssistantMessage("429 Too Many Requests #3")},
		})

		_ = h.Session.Prompt(ctx, "hello")

		retryStarts := h.EventsOfType("auto_retry_start")
		retryEnds := h.EventsOfType("auto_retry_end")
		if len(retryStarts) != 2 {
			t.Fatalf("expected 2 auto_retry_start events, got %d", len(retryStarts))
		}
		if len(retryEnds) != 1 {
			t.Fatalf("expected 1 auto_retry_end event, got %d", len(retryEnds))
		}
		re := retryEnds[0].(*core.AgentSessionEventAutoRetryEnd)
		if re.Success || re.Attempt != 2 || re.FinalError == nil {
			t.Fatalf("expected auto_retry_end failure at attempt 2 with FinalError, got %+v", re)
		}
	})

	t.Run("does not retry when auto-retry is disabled", func(t *testing.T) {
		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Settings: tsasreFastRetrySettings(3, false),
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{Message: tsasreErrorAssistantMessage("429 Too Many Requests")},
		})

		_ = h.Session.Prompt(ctx, "hello")

		if n := len(h.EventsOfType("auto_retry_start")); n != 0 {
			t.Fatalf("expected 0 auto_retry_start events when disabled, got %d", n)
		}
		agentEnds := h.EventsOfType("agent_end")
		if len(agentEnds) != 1 || agentEnds[0].(*core.AgentSessionEventAgentEnd).WillRetry {
			t.Fatalf("expected 1 agent_end with WillRetry=false, got %#v", agentEnds)
		}
	})

	t.Run("does not retry non-retryable errors", func(t *testing.T) {
		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Settings: tsasreFastRetrySettings(3, true),
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{Message: tsasreErrorAssistantMessage("invalid_api_key: bad credentials")},
		})

		_ = h.Session.Prompt(ctx, "hello")

		if n := len(h.EventsOfType("auto_retry_start")); n != 0 {
			t.Fatalf("expected 0 retries for invalid_api_key, got %d", n)
		}
		agentEnds := h.EventsOfType("agent_end")
		if len(agentEnds) != 1 || agentEnds[0].(*core.AgentSessionEventAgentEnd).WillRetry {
			t.Fatalf("expected WillRetry=false, got %#v", agentEnds)
		}
	})

	t.Run("aborts a scheduled retry sleep via abortRetry", func(t *testing.T) {
		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Settings: &core.Settings{
				Retry: &core.RetrySettings{
					Enabled:         tsasreBoolPtr(true),
					MaxRetries:      tsasreIntPtr(3),
					BaseDelayMs:     tsasreFloatPtr(5000),
					MaxAgentDelayMs: tsasreFloatPtr(5000),
				},
			},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		retryStarted := make(chan struct{})
		var once sync.Once
		unsub := h.Session.Subscribe(func(ev core.AgentSessionEvent) {
			if _, ok := ev.(*core.AgentSessionEventAutoRetryStart); ok {
				once.Do(func() { close(retryStarted) })
			}
		})
		defer unsub()

		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{Message: tsasreErrorAssistantMessage("503 Service Unavailable")},
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("should not run"), nil)},
		})

		errCh := make(chan error, 1)
		go func() {
			errCh <- h.Session.Prompt(ctx, "hello")
		}()

		select {
		case <-retryStarted:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for auto_retry_start")
		}

		h.Session.AbortRetry()
		<-errCh

		retryEnds := h.EventsOfType("auto_retry_end")
		if len(retryEnds) != 1 {
			t.Fatalf("expected 1 auto_retry_end, got %d", len(retryEnds))
		}
		re := retryEnds[0].(*core.AgentSessionEventAutoRetryEnd)
		if re.Success || re.FinalError == nil || *re.FinalError != "Retry cancelled" {
			t.Fatalf("expected FinalError 'Retry cancelled', got %+v (err=%v)", re, re.FinalError)
		}
	})

	t.Run("retries a failed turn and executes tool calls from the retried response", func(t *testing.T) {
		echoTool := &agent.AgentTool{
			Name:        "echo",
			Label:       "Echo",
			Description: "Echo test",
			Parameters:  typebox.Object(nil),
			Execute: func(_ context.Context, _ string, _ any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
				return agent.AgentToolResult{
					Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "echo ok"}},
				}, nil
			},
		}

		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Tools:    []*agent.AgentTool{echoTool},
			Settings: tsasreFastRetrySettings(3, true),
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		stopTool := ai.StopReasonToolUse
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{Message: tsasreErrorAssistantMessage("429 Too Many Requests")},
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantBlock{Block: providers.FauxToolCall("echo", jsonx.NewObject(), nil)},
					&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
				),
			},
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText("done after retry tool"), nil),
			},
		})

		if err := h.Session.Prompt(ctx, "run tool"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}

		tr, err := testkit.GetToolResult(h, "echo")
		if err != nil {
			t.Fatalf("GetToolResult: %v", err)
		}
		if got := testkit.GetMessageText(tr); got != "echo ok" {
			t.Fatalf("expected 'echo ok', got %q", got)
		}
		texts := testkit.GetAssistantTexts(h)
		if len(texts) == 0 || texts[len(texts)-1] != "done after retry tool" {
			t.Fatalf("unexpected assistant texts: %#v", texts)
		}
	})

	t.Run("emits exact event sequence for single prompt", func(t *testing.T) {
		h, err := testkit.CreateSuiteHarness(ctx)
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText("hello world"), nil),
			},
		})

		if err := h.Session.Prompt(ctx, "hi"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}

		var seq []string
		for _, ev := range h.Events() {
			switch e := ev.(type) {
			case *core.AgentSessionEventAgentStart:
				seq = append(seq, "agent_start")
			case *core.AgentSessionEventTurnStart:
				seq = append(seq, "turn_start")
			case *core.AgentSessionEventMessageStart:
				role := "unknown"
				switch e.Message.(type) {
				case *ai.UserMessage:
					role = "user"
				case *ai.AssistantMessage:
					role = "assistant"
				case *ai.SystemMessage:
					role = "system"
				}
				seq = append(seq, "message_start:"+role)
			case *core.AgentSessionEventMessageEnd:
				role := "unknown"
				switch e.Message.(type) {
				case *ai.UserMessage:
					role = "user"
				case *ai.AssistantMessage:
					role = "assistant"
				case *ai.SystemMessage:
					role = "system"
				}
				seq = append(seq, "message_end:"+role)
			case *core.AgentSessionEventTurnEnd:
				seq = append(seq, "turn_end")
			case *core.AgentSessionEventAgentEnd:
				seq = append(seq, "agent_end")
			}
		}

		// Filter out optional initial system message events.
		var coreSeq []string
		for _, item := range seq {
			if !strings.HasSuffix(item, ":system") {
				coreSeq = append(coreSeq, item)
			}
		}
		expected := []string{
			"agent_start",
			"turn_start",
			"message_start:user",
			"message_end:user",
			"message_start:assistant",
			"message_end:assistant",
			"turn_end",
			"agent_end",
		}
		if strings.Join(coreSeq, ",") != strings.Join(expected, ",") {
			t.Fatalf("expected event sequence %v, got %v", expected, coreSeq)
		}
	})

	t.Run("emits tool execution events in order around tool calls", func(t *testing.T) {
		pingTool := &agent.AgentTool{
			Name:        "ping",
			Label:       "Ping",
			Description: "Ping",
			Parameters:  typebox.Object(nil),
			Execute: func(_ context.Context, _ string, _ any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
				return agent.AgentToolResult{
					Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "pong"}},
				}, nil
			},
		}

		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Tools: []*agent.AgentTool{pingTool},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		stopTool := ai.StopReasonToolUse
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantBlock{Block: providers.FauxToolCall("ping", jsonx.NewObject(), nil)},
					&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
				),
			},
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText("ponged"), nil),
			},
		})

		if err := h.Session.Prompt(ctx, "ping"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}

		var toolSeq []string
		for _, ev := range h.Events() {
			switch e := ev.(type) {
			case *core.AgentSessionEventToolExecutionStart:
				toolSeq = append(toolSeq, "tool_execution_start:"+e.ToolName)
			case *core.AgentSessionEventToolExecutionEnd:
				toolSeq = append(toolSeq, "tool_execution_end:"+e.ToolName)
			case *core.AgentSessionEventMessageStart:
				if _, ok := e.Message.(*ai.ToolResultMessage); ok {
					toolSeq = append(toolSeq, "message_start:toolResult")
				}
			case *core.AgentSessionEventMessageEnd:
				if _, ok := e.Message.(*ai.ToolResultMessage); ok {
					toolSeq = append(toolSeq, "message_end:toolResult")
				}
			}
		}
		expected := []string{
			"tool_execution_start:ping",
			"tool_execution_end:ping",
			"message_start:toolResult",
			"message_end:toolResult",
		}
		if strings.Join(toolSeq, ",") != strings.Join(expected, ",") {
			t.Fatalf("expected tool event order %v, got %v", expected, toolSeq)
		}
	})

	t.Run("emits streaming thinking, text, and toolcall deltas in message_update", func(t *testing.T) {
		pingTool := &agent.AgentTool{
			Name:        "ping",
			Label:       "Ping",
			Description: "Ping",
			Parameters:  typebox.Object(nil),
			Execute: func(_ context.Context, _ string, _ any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
				return agent.AgentToolResult{
					Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "pong"}},
				}, nil
			},
		}

		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Tools: []*agent.AgentTool{pingTool},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		stopTool := ai.StopReasonToolUse
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantBlocks{
						providers.FauxThinking("let me think"),
						providers.FauxToolCall("ping", jsonx.NewObject(), nil),
					},
					&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
				),
			},
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText("final answer"), nil),
			},
		})

		if err := h.Session.Prompt(ctx, "stream deltas"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}

		var sawThinkingDelta, sawToolcallDelta, sawTextDelta bool
		for _, ev := range h.EventsOfType("message_update") {
			mu := ev.(*core.AgentSessionEventMessageUpdate)
			switch mu.AssistantMessageEvent.(type) {
			case *ai.AssistantMessageEventThinkingDelta:
				sawThinkingDelta = true
			case *ai.AssistantMessageEventToolcallDelta:
				sawToolcallDelta = true
			case *ai.AssistantMessageEventTextDelta:
				sawTextDelta = true
			}
		}
		if !sawThinkingDelta || !sawToolcallDelta || !sawTextDelta {
			t.Fatalf("expected thinking/toolcall/text deltas, got thinking=%v toolcall=%v text=%v", sawThinkingDelta, sawToolcallDelta, sawTextDelta)
		}
	})

	t.Run("emits agent_end when model returns an error", func(t *testing.T) {
		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Settings: tsasreFastRetrySettings(0, false),
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{Message: tsasreErrorAssistantMessage("fatal model error")},
		})

		_ = h.Session.Prompt(ctx, "fail")

		agentEnds := h.EventsOfType("agent_end")
		if len(agentEnds) != 1 {
			t.Fatalf("expected 1 agent_end, got %d", len(agentEnds))
		}
		ae := agentEnds[0].(*core.AgentSessionEventAgentEnd)
		if ae.WillRetry {
			t.Fatal("expected WillRetry=false")
		}
		var sawErr bool
		for _, m := range ae.Messages {
			if am, ok := m.(*ai.AssistantMessage); ok && am != nil && am.StopReason == ai.StopReasonError {
				sawErr = true
			}
		}
		if !sawErr {
			t.Fatal("expected error AssistantMessage inside agent_end.Messages")
		}
	})

	t.Run("emits agent_end with aborted stopReason on abort during streaming", func(t *testing.T) {
		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Settings: tsasreFastRetrySettings(0, false),
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		reqStarted := make(chan struct{})
		var once sync.Once
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseFactory(func(reqCtx context.Context, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
				once.Do(func() { close(reqStarted) })
				<-reqCtx.Done()
				stopAbort := ai.StopReasonAborted
				return providers.FauxAssistantMessage(
					providers.FauxAssistantText(""),
					&providers.FauxAssistantMessageOptions{
						StopReason:   &stopAbort,
						ErrorMessage: tsasreStrPtr("Request was aborted"),
					},
				), nil
			}),
		})

		errCh := make(chan error, 1)
		go func() {
			errCh <- h.Session.Prompt(ctx, "abort me")
		}()

		select {
		case <-reqStarted:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for LLM request")
		}

		if err := h.Session.Abort(ctx); err != nil {
			t.Fatalf("Abort: %v", err)
		}
		<-errCh

		agentEnds := h.EventsOfType("agent_end")
		if len(agentEnds) != 1 {
			t.Fatalf("expected 1 agent_end after abort, got %d", len(agentEnds))
		}
		ae := agentEnds[0].(*core.AgentSessionEventAgentEnd)
		var sawAborted bool
		for _, m := range ae.Messages {
			if am, ok := m.(*ai.AssistantMessage); ok && am != nil && am.StopReason == ai.StopReasonAborted {
				sawAborted = true
			}
		}
		if !sawAborted {
			t.Fatalf("expected AssistantMessage with StopReasonAborted in agent_end, got %#v", ae.Messages)
		}
	})

	t.Run("extension message_end hooks during retry and events", func(t *testing.T) {
		t.Skip("barebone excludes extension message_end hooks")
	})
}
