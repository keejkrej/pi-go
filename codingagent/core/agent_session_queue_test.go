// Ported from packages/coding-agent/test/suite/agent-session-queue.test.ts (pi v1.5.2).

package core_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/png"
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

func tsasqTinyPngBase64(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func tsasqNewBlockingTool() (*agent.AgentTool, <-chan struct{}, func()) {
	started := make(chan struct{})
	release := make(chan struct{})
	var startOnce, releaseOnce sync.Once
	tool := &agent.AgentTool{
		Name:        "gate",
		Label:       "Gate",
		Description: "Blocks until test releases gate",
		Parameters:  typebox.Object(nil),
		Execute: func(_ context.Context, _ string, _ any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
			startOnce.Do(func() { close(started) })
			<-release
			return agent.AgentToolResult{
				Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "gate opened"}},
			}, nil
		},
	}
	return tool, started, func() {
		releaseOnce.Do(func() { close(release) })
	}
}

func TestAgentSessionQueueCharacterization(t *testing.T) {
	ctx := context.Background()

	t.Run("delivers follow-up messages after a turn finishes", func(t *testing.T) {
		gate, started, release := tsasqNewBlockingTool()
		defer release()

		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Tools: []*agent.AgentTool{gate},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		stopTool := ai.StopReasonToolUse
		var seenFollowUp bool
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantBlock{Block: providers.FauxToolCall("gate", jsonx.NewObject(), nil)},
					&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
				),
			},
			providers.FauxResponseFactory(func(_ context.Context, tc *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
				for _, m := range tc.Messages {
					if strings.Contains(testkit.GetMessageText(m), "follow up later") {
						t.Fatal("follow-up should not be injected before first turn completes")
					}
				}
				return providers.FauxAssistantMessage(providers.FauxAssistantText("first done"), nil), nil
			}),
			providers.FauxResponseFactory(func(_ context.Context, tc *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
				for _, m := range tc.Messages {
					if strings.Contains(testkit.GetMessageText(m), "follow up later") {
						seenFollowUp = true
					}
				}
				return providers.FauxAssistantMessage(providers.FauxAssistantText("follow up done"), nil), nil
			}),
		})

		errCh := make(chan error, 1)
		go func() {
			errCh <- h.Session.Prompt(ctx, "run gate")
		}()

		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for gate tool")
		}

		if _, err := h.Session.FollowUp(ctx, "follow up later", nil); err != nil {
			t.Fatalf("FollowUp: %v", err)
		}
		release()

		if err := <-errCh; err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		if !seenFollowUp {
			t.Fatal("expected follow-up message to trigger subsequent turn")
		}
		assistantTexts := testkit.GetAssistantTexts(h)
		if len(assistantTexts) != 3 || assistantTexts[1] != "first done" || assistantTexts[2] != "follow up done" {
			t.Fatalf("unexpected assistant texts: %#v", assistantTexts)
		}
	})

	t.Run("delivers multiple steering messages one at a time when steeringMode is one-at-a-time", func(t *testing.T) {
		gate, started, release := tsasqNewBlockingTool()
		defer release()

		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Tools: []*agent.AgentTool{gate},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		h.Session.SetSteeringMode(agent.QueueModeOneAtATime)
		stopTool := ai.StopReasonToolUse
		var turnRequests [][]string
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantBlock{Block: providers.FauxToolCall("gate", jsonx.NewObject(), nil)},
					&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
				),
			},
			providers.FauxResponseFactory(func(_ context.Context, tc *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
				var users []string
				for _, m := range tc.Messages {
					if um, ok := m.(*ai.UserMessage); ok && um != nil {
						users = append(users, testkit.GetMessageText(um))
					}
				}
				turnRequests = append(turnRequests, users)
				return providers.FauxAssistantMessage(providers.FauxAssistantText("after steer 1"), nil), nil
			}),
			providers.FauxResponseFactory(func(_ context.Context, tc *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
				var users []string
				for _, m := range tc.Messages {
					if um, ok := m.(*ai.UserMessage); ok && um != nil {
						users = append(users, testkit.GetMessageText(um))
					}
				}
				turnRequests = append(turnRequests, users)
				return providers.FauxAssistantMessage(providers.FauxAssistantText("after steer 2"), nil), nil
			}),
		})

		errCh := make(chan error, 1)
		go func() {
			errCh <- h.Session.Prompt(ctx, "start")
		}()

		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for gate")
		}

		if _, err := h.Session.Steer(ctx, "steer 1", nil); err != nil {
			t.Fatalf("Steer 1: %v", err)
		}
		if _, err := h.Session.Steer(ctx, "steer 2", nil); err != nil {
			t.Fatalf("Steer 2: %v", err)
		}
		release()

		if err := <-errCh; err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		if len(turnRequests) != 2 {
			t.Fatalf("expected 2 post-gate LLM calls, got %d (%#v)", len(turnRequests), turnRequests)
		}
		if strings.Join(turnRequests[0], "|") != "start|steer 1" {
			t.Fatalf("expected first post-gate turn to see only [start, steer 1], got %#v", turnRequests[0])
		}
		if strings.Join(turnRequests[1], "|") != "start|steer 1|steer 2" {
			t.Fatalf("expected second post-gate turn to see [start, steer 1, steer 2], got %#v", turnRequests[1])
		}
	})

	t.Run("delivers multiple follow-up messages one at a time when followUpMode is one-at-a-time", func(t *testing.T) {
		gate, started, release := tsasqNewBlockingTool()
		defer release()

		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Tools: []*agent.AgentTool{gate},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		h.Session.SetFollowUpMode(agent.QueueModeOneAtATime)
		stopTool := ai.StopReasonToolUse
		var followUpRequests [][]string
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantBlock{Block: providers.FauxToolCall("gate", jsonx.NewObject(), nil)},
					&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
				),
			},
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText("initial done"), nil),
			},
			providers.FauxResponseFactory(func(_ context.Context, tc *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
				var users []string
				for _, m := range tc.Messages {
					if um, ok := m.(*ai.UserMessage); ok && um != nil {
						users = append(users, testkit.GetMessageText(um))
					}
				}
				followUpRequests = append(followUpRequests, users)
				return providers.FauxAssistantMessage(providers.FauxAssistantText("after fu 1"), nil), nil
			}),
			providers.FauxResponseFactory(func(_ context.Context, tc *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
				var users []string
				for _, m := range tc.Messages {
					if um, ok := m.(*ai.UserMessage); ok && um != nil {
						users = append(users, testkit.GetMessageText(um))
					}
				}
				followUpRequests = append(followUpRequests, users)
				return providers.FauxAssistantMessage(providers.FauxAssistantText("after fu 2"), nil), nil
			}),
		})

		errCh := make(chan error, 1)
		go func() {
			errCh <- h.Session.Prompt(ctx, "start")
		}()

		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for gate")
		}

		if _, err := h.Session.FollowUp(ctx, "fu 1", nil); err != nil {
			t.Fatalf("FollowUp 1: %v", err)
		}
		if _, err := h.Session.FollowUp(ctx, "fu 2", nil); err != nil {
			t.Fatalf("FollowUp 2: %v", err)
		}
		release()

		if err := <-errCh; err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		if len(followUpRequests) != 2 {
			t.Fatalf("expected 2 follow-up turns, got %d (%#v)", len(followUpRequests), followUpRequests)
		}
		if strings.Join(followUpRequests[0], "|") != "start|fu 1" {
			t.Fatalf("expected first follow-up turn to see [start, fu 1], got %#v", followUpRequests[0])
		}
		if strings.Join(followUpRequests[1], "|") != "start|fu 1|fu 2" {
			t.Fatalf("expected second follow-up turn to see [start, fu 1, fu 2], got %#v", followUpRequests[1])
		}
	})

	t.Run("batches all steering messages when steeringMode is all", func(t *testing.T) {
		gate, started, release := tsasqNewBlockingTool()
		defer release()

		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Tools: []*agent.AgentTool{gate},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		h.Session.SetSteeringMode(agent.QueueModeAll)
		stopTool := ai.StopReasonToolUse
		var batchedUsers []string
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantBlock{Block: providers.FauxToolCall("gate", jsonx.NewObject(), nil)},
					&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
				),
			},
			providers.FauxResponseFactory(func(_ context.Context, tc *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
				for _, m := range tc.Messages {
					if um, ok := m.(*ai.UserMessage); ok && um != nil {
						batchedUsers = append(batchedUsers, testkit.GetMessageText(um))
					}
				}
				return providers.FauxAssistantMessage(providers.FauxAssistantText("batched steer done"), nil), nil
			}),
		})

		errCh := make(chan error, 1)
		go func() {
			errCh <- h.Session.Prompt(ctx, "start")
		}()

		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for gate")
		}

		_, _ = h.Session.Steer(ctx, "steer A", nil)
		_, _ = h.Session.Steer(ctx, "steer B", nil)
		release()

		if err := <-errCh; err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		if strings.Join(batchedUsers, "|") != "start|steer A|steer B" {
			t.Fatalf("expected batched steering users [start, steer A, steer B], got %#v", batchedUsers)
		}
		if h.Faux.State.CallCount != 2 {
			t.Fatalf("expected 2 total LLM calls when batching steering, got %d", h.Faux.State.CallCount)
		}
	})

	t.Run("batches all follow-up messages when followUpMode is all", func(t *testing.T) {
		gate, started, release := tsasqNewBlockingTool()
		defer release()

		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Tools: []*agent.AgentTool{gate},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		h.Session.SetFollowUpMode(agent.QueueModeAll)
		stopTool := ai.StopReasonToolUse
		var batchedUsers []string
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantBlock{Block: providers.FauxToolCall("gate", jsonx.NewObject(), nil)},
					&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
				),
			},
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText("initial done"), nil),
			},
			providers.FauxResponseFactory(func(_ context.Context, tc *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
				for _, m := range tc.Messages {
					if um, ok := m.(*ai.UserMessage); ok && um != nil {
						batchedUsers = append(batchedUsers, testkit.GetMessageText(um))
					}
				}
				return providers.FauxAssistantMessage(providers.FauxAssistantText("batched fu done"), nil), nil
			}),
		})

		errCh := make(chan error, 1)
		go func() {
			errCh <- h.Session.Prompt(ctx, "start")
		}()

		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for gate")
		}

		_, _ = h.Session.FollowUp(ctx, "fu A", nil)
		_, _ = h.Session.FollowUp(ctx, "fu B", nil)
		release()

		if err := <-errCh; err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		if strings.Join(batchedUsers, "|") != "start|fu A|fu B" {
			t.Fatalf("expected batched followUp users [start, fu A, fu B], got %#v", batchedUsers)
		}
		if h.Faux.State.CallCount != 3 {
			t.Fatalf("expected 3 total LLM calls when batching follow-ups, got %d", h.Faux.State.CallCount)
		}
	})

	t.Run("delivers sendCustomMessage steer immediately during streaming", func(t *testing.T) {
		gate, started, release := tsasqNewBlockingTool()
		defer release()

		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Tools: []*agent.AgentTool{gate},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		stopTool := ai.StopReasonToolUse
		var sawSteerCustom bool
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantBlock{Block: providers.FauxToolCall("gate", jsonx.NewObject(), nil)},
					&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
				),
			},
			providers.FauxResponseFactory(func(_ context.Context, tc *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
				for _, m := range tc.Messages {
					if strings.Contains(testkit.GetMessageText(m), "urgent custom steer") {
						sawSteerCustom = true
					}
				}
				return providers.FauxAssistantMessage(providers.FauxAssistantText("steered custom"), nil), nil
			}),
		})

		errCh := make(chan error, 1)
		go func() {
			errCh <- h.Session.Prompt(ctx, "start")
		}()

		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for gate")
		}

		if err := h.Session.SendCustomMessage(ctx, "alert", "urgent custom steer", true, nil, &core.SendCustomMessageOptions{
			DeliverAs: "steer",
		}); err != nil {
			t.Fatalf("SendCustomMessage(steer): %v", err)
		}
		release()

		if err := <-errCh; err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		if !sawSteerCustom {
			t.Fatal("expected steered custom message in same-run continuation LLM request")
		}
	})

	t.Run("delivers sendCustomMessage followUp after active turn finishes", func(t *testing.T) {
		gate, started, release := tsasqNewBlockingTool()
		defer release()

		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Tools: []*agent.AgentTool{gate},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		stopTool := ai.StopReasonToolUse
		var sawFollowUpCustom bool
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantBlock{Block: providers.FauxToolCall("gate", jsonx.NewObject(), nil)},
					&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
				),
			},
			providers.FauxResponseFactory(func(_ context.Context, tc *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
				for _, m := range tc.Messages {
					if strings.Contains(testkit.GetMessageText(m), "custom follow up") {
						t.Fatal("custom followUp should wait until turn completes")
					}
				}
				return providers.FauxAssistantMessage(providers.FauxAssistantText("first done"), nil), nil
			}),
			providers.FauxResponseFactory(func(_ context.Context, tc *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
				for _, m := range tc.Messages {
					if strings.Contains(testkit.GetMessageText(m), "custom follow up") {
						sawFollowUpCustom = true
					}
				}
				return providers.FauxAssistantMessage(providers.FauxAssistantText("custom fu done"), nil), nil
			}),
		})

		errCh := make(chan error, 1)
		go func() {
			errCh <- h.Session.Prompt(ctx, "start")
		}()

		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for gate")
		}

		if err := h.Session.SendCustomMessage(ctx, "note", "custom follow up", true, nil, &core.SendCustomMessageOptions{
			DeliverAs: "followUp",
		}); err != nil {
			t.Fatalf("SendCustomMessage(followUp): %v", err)
		}
		release()

		if err := <-errCh; err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		if !sawFollowUpCustom {
			t.Fatal("expected followUp custom message in subsequent turn")
		}
	})

	t.Run("persists sendCustomMessage nextTurn without triggering a continuation until next prompt", func(t *testing.T) {
		gate, started, release := tsasqNewBlockingTool()
		defer release()

		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Tools: []*agent.AgentTool{gate},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		stopTool := ai.StopReasonToolUse
		var sawInNextPrompt bool
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantBlock{Block: providers.FauxToolCall("gate", jsonx.NewObject(), nil)},
					&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
				),
			},
			providers.FauxResponseFactory(func(_ context.Context, tc *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
				for _, m := range tc.Messages {
					if strings.Contains(testkit.GetMessageText(m), "for next turn") {
						t.Fatal("nextTurn custom message should not be sent mid-run")
					}
				}
				return providers.FauxAssistantMessage(providers.FauxAssistantText("run1 done"), nil), nil
			}),
			providers.FauxResponseFactory(func(_ context.Context, tc *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
				for _, m := range tc.Messages {
					if strings.Contains(testkit.GetMessageText(m), "for next turn") {
						sawInNextPrompt = true
					}
				}
				return providers.FauxAssistantMessage(providers.FauxAssistantText("run2 done"), nil), nil
			}),
		})

		errCh := make(chan error, 1)
		go func() {
			errCh <- h.Session.Prompt(ctx, "run 1")
		}()

		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for gate")
		}

		if err := h.Session.SendCustomMessage(ctx, "note", "for next turn", true, nil, &core.SendCustomMessageOptions{
			DeliverAs: "nextTurn",
		}); err != nil {
			t.Fatalf("SendCustomMessage(nextTurn): %v", err)
		}
		release()

		if err := <-errCh; err != nil {
			t.Fatalf("Prompt 1: %v", err)
		}
		if h.Faux.State.CallCount != 2 {
			t.Fatalf("expected no extra turn for nextTurn custom message, got %d LLM calls", h.Faux.State.CallCount)
		}

		if err := h.Session.Prompt(ctx, "run 2"); err != nil {
			t.Fatalf("Prompt 2: %v", err)
		}
		if !sawInNextPrompt {
			t.Fatal("expected flushed nextTurn custom message to be visible on next user prompt")
		}
	})

	t.Run("PendingMessageCount drops to 0 before message_start for queued steering", func(t *testing.T) {
		gate, started, release := tsasqNewBlockingTool()
		defer release()

		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Tools: []*agent.AgentTool{gate},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		pendingAtSteerStart := -1
		unsub := h.Session.Subscribe(func(ev core.AgentSessionEvent) {
			if ms, ok := ev.(*core.AgentSessionEventMessageStart); ok && ms != nil {
				if testkit.GetMessageText(ms.Message) == "count check" {
					pendingAtSteerStart = h.Session.PendingMessageCount()
				}
			}
		})
		defer unsub()

		stopTool := ai.StopReasonToolUse
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantBlock{Block: providers.FauxToolCall("gate", jsonx.NewObject(), nil)},
					&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
				),
			},
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText("done"), nil),
			},
		})

		errCh := make(chan error, 1)
		go func() {
			errCh <- h.Session.Prompt(ctx, "start")
		}()

		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for gate")
		}

		if _, err := h.Session.Steer(ctx, "count check", nil); err != nil {
			t.Fatalf("Steer: %v", err)
		}
		if got := h.Session.PendingMessageCount(); got != 1 {
			release()
			t.Fatalf("expected PendingMessageCount == 1 after Steer, got %d", got)
		}
		release()

		if err := <-errCh; err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		if pendingAtSteerStart != 0 {
			t.Fatalf("expected PendingMessageCount == 0 at steering message_start, got %d", pendingAtSteerStart)
		}
	})

	t.Run("handles image-only steering messages", func(t *testing.T) {
		gate, started, release := tsasqNewBlockingTool()
		defer release()

		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Tools: []*agent.AgentTool{gate},
			Models: []providers.FauxModelDefinition{
				{
					Id:    "faux-vision",
					Input: []string{"text", "image"},
				},
			},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		pngData := tsasqTinyPngBase64(t)
		stopTool := ai.StopReasonToolUse
		var sawSteerImage bool
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantBlock{Block: providers.FauxToolCall("gate", jsonx.NewObject(), nil)},
					&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
				),
			},
			providers.FauxResponseFactory(func(_ context.Context, tc *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
				for _, m := range tc.Messages {
					if um, ok := m.(*ai.UserMessage); ok && um != nil {
						for _, blk := range um.Content.Blocks {
							if img, ok := blk.(*ai.ImageContent); ok && img != nil && img.Data == pngData {
								sawSteerImage = true
							}
						}
					}
				}
				return providers.FauxAssistantMessage(providers.FauxAssistantText("saw steer img"), nil), nil
			}),
		})

		errCh := make(chan error, 1)
		go func() {
			errCh <- h.Session.Prompt(ctx, "start")
		}()

		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for gate")
		}

		if _, err := h.Session.Steer(ctx, "", []*ai.ImageContent{
			{Type: "image", Data: pngData, MimeType: "image/png"},
		}); err != nil {
			release()
			t.Fatalf("Steer(image-only): %v", err)
		}
		release()

		if err := <-errCh; err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		if !sawSteerImage {
			t.Fatal("expected image-only steering message to reach LLM transcript")
		}
	})

	t.Run("runs queued steering before follow-up when extension commands queue both", func(t *testing.T) {
		t.Skip("barebone excludes extension commands")
	})

	t.Run("passes steering messages through extension input handlers", func(t *testing.T) {
		t.Skip("barebone excludes extension input hooks")
	})

	t.Run("runs extension agent_end hooks once queued continuations complete", func(t *testing.T) {
		t.Skip("barebone excludes extension agent_end hooks")
	})
}
