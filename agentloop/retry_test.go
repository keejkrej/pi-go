package agentloop

import (
	"context"
	"testing"
	"time"
)

func TestIsRetryableAssistantError(t *testing.T) {
	cases := []struct {
		name string
		msg  *AssistantMessage
		want bool
	}{
		{"nil", nil, false},
		{"not error", &AssistantMessage{StopReason: StopReasonStop}, false},
		{"no message", &AssistantMessage{StopReason: StopReasonError}, false},
		{"rate limit", &AssistantMessage{StopReason: StopReasonError, ErrorMessage: "HTTP 429: rate limit exceeded"}, true},
		{"overloaded", &AssistantMessage{StopReason: StopReasonError, ErrorMessage: "Overloaded"}, true},
		{"server error", &AssistantMessage{StopReason: StopReasonError, ErrorMessage: "HTTP 503: service unavailable"}, true},
		{"premature end", &AssistantMessage{StopReason: StopReasonError, ErrorMessage: "Stream ended without finish_reason"}, true},
		{"socket", &AssistantMessage{StopReason: StopReasonError, ErrorMessage: "socket hang up"}, true},
		{"quota", &AssistantMessage{StopReason: StopReasonError, ErrorMessage: "429 insufficient_quota"}, false},
		{"billing", &AssistantMessage{StopReason: StopReasonError, ErrorMessage: "billing hard limit reached"}, false},
		{"unrelated", &AssistantMessage{StopReason: StopReasonError, ErrorMessage: "invalid schema"}, false},
		{"aborted", &AssistantMessage{StopReason: StopReasonAborted, ErrorMessage: "Request was aborted"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsRetryableAssistantError(tc.msg); got != tc.want {
				t.Fatalf("IsRetryableAssistantError(%v) = %v, want %v", tc.msg, got, tc.want)
			}
		})
	}
}

func fauxError(msg string) *AssistantMessage {
	return &AssistantMessage{
		StopReason:   StopReasonError,
		ErrorMessage: msg,
		Timestamp:    time.Now().UnixMilli(),
	}
}

func retryConfig(maxAttempts int) *AgentLoopConfig {
	return &AgentLoopConfig{
		Model:        &Model{ID: "m", Api: "faux", Provider: "faux"},
		ConvertToLlm: identityConvert,
		AutoRetry:    &AutoRetryConfig{MaxAttempts: maxAttempts, BaseDelay: time.Millisecond},
	}
}

func TestAgentLoop_AutoRetrySucceeds(t *testing.T) {
	agentCtx := &AgentContext{}
	streamFn := FauxStreamFn(
		fauxError("HTTP 429: too many requests"),
		FauxText("recovered"),
	)

	stream := AgentLoop(context.Background(), []AgentMessage{NewUserText("hi")},
		agentCtx, retryConfig(2), streamFn)

	var retries []*AutoRetryEvent
	for ev := range stream.Events() {
		if r, ok := ev.(*AutoRetryEvent); ok {
			retries = append(retries, r)
		}
	}
	messages := stream.Result()

	if len(retries) != 1 {
		t.Fatalf("auto retry events = %d, want 1", len(retries))
	}
	if retries[0].Attempt != 1 || retries[0].MaxAttempts != 2 {
		t.Fatalf("retry event = %+v", retries[0])
	}
	// Result: user prompt + successful assistant only; the errored assistant
	// message was dropped.
	if len(messages) != 2 {
		t.Fatalf("result messages = %d, want 2 (error dropped): %#v", len(messages), messages)
	}
	final, ok := messages[1].(*AssistantMessage)
	if !ok || final.StopReason != StopReasonStop {
		t.Fatalf("final message = %#v", messages[1])
	}
}

func TestAgentLoop_AutoRetryExhausted(t *testing.T) {
	agentCtx := &AgentContext{}
	streamFn := FauxStreamFn(fauxError("HTTP 503: service unavailable"))

	stream := AgentLoop(context.Background(), []AgentMessage{NewUserText("hi")},
		agentCtx, retryConfig(2), streamFn)

	retryCount := 0
	for ev := range stream.Events() {
		if _, ok := ev.(*AutoRetryEvent); ok {
			retryCount++
		}
	}
	messages := stream.Result()

	if retryCount != 2 {
		t.Fatalf("auto retry events = %d, want 2", retryCount)
	}
	last, ok := messages[len(messages)-1].(*AssistantMessage)
	if !ok || last.StopReason != StopReasonError {
		t.Fatalf("expected final error message, got %#v", messages[len(messages)-1])
	}
}

func TestAgentLoop_NoRetryForNonRetryable(t *testing.T) {
	agentCtx := &AgentContext{}
	streamFn := FauxStreamFn(fauxError("insufficient_quota"))

	stream := AgentLoop(context.Background(), []AgentMessage{NewUserText("hi")},
		agentCtx, retryConfig(3), streamFn)

	for ev := range stream.Events() {
		if _, ok := ev.(*AutoRetryEvent); ok {
			t.Fatal("unexpected AutoRetryEvent for non-retryable error")
		}
	}
	messages := stream.Result()
	last := messages[len(messages)-1].(*AssistantMessage)
	if last.StopReason != StopReasonError {
		t.Fatalf("final stop reason = %q", last.StopReason)
	}
}

func TestAutoRetryConfig_Delay(t *testing.T) {
	c := &AutoRetryConfig{BaseDelay: time.Second, MaxDelay: 5 * time.Second}
	if got := c.delay(1); got != time.Second {
		t.Fatalf("delay(1) = %v", got)
	}
	if got := c.delay(2); got != 2*time.Second {
		t.Fatalf("delay(2) = %v", got)
	}
	if got := c.delay(4); got != 5*time.Second {
		t.Fatalf("delay(4) = %v, want capped at 5s", got)
	}
	d := &AutoRetryConfig{}
	if got := d.delay(1); got != DefaultRetryBaseDelay {
		t.Fatalf("default delay = %v", got)
	}
}
