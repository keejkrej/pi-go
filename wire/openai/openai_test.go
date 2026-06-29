package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	ag "github.com/keejkrej/pi-go/agentloop"
)

const cannedSSE = `data: {"id":"chatcmpl-1","model":"gpt-test","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"}}]}

data: {"id":"chatcmpl-1","model":"gpt-test","choices":[{"index":0,"delta":{"content":", world"}}]}

data: {"id":"chatcmpl-1","model":"gpt-test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"get_weather","arguments":"{\"loc"}}]}}]}

data: {"id":"chatcmpl-1","model":"gpt-test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"ation\":\"SF\"}"}}]}}]}

data: {"id":"chatcmpl-1","model":"gpt-test","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":100,"completion_tokens":20,"prompt_tokens_details":{"cached_tokens":40}}}

data: [DONE]

`

func newTestModel(baseURL string) *ag.Model {
	return &ag.Model{
		ID:       "gpt-test",
		Api:      "openai-completions",
		Provider: "openai",
		BaseURL:  baseURL,
		Input:    []string{"text"},
		Cost:     ag.ModelCost{Input: 1, Output: 2, CacheRead: 0.5, CacheWrite: 0},
	}
}

func drain(stream *ag.AssistantMessageEventStream) []ag.AssistantMessageEvent {
	var events []ag.AssistantMessageEvent
	for ev := range stream.Events() {
		events = append(events, ev)
	}
	return events
}

func eventKinds(events []ag.AssistantMessageEvent) []string {
	var kinds []string
	for _, ev := range events {
		switch ev.(type) {
		case *ag.StartEvent:
			kinds = append(kinds, "start")
		case *ag.TextStartEvent:
			kinds = append(kinds, "text_start")
		case *ag.TextDeltaEvent:
			kinds = append(kinds, "text_delta")
		case *ag.TextEndEvent:
			kinds = append(kinds, "text_end")
		case *ag.ThinkingStartEvent:
			kinds = append(kinds, "thinking_start")
		case *ag.ThinkingDeltaEvent:
			kinds = append(kinds, "thinking_delta")
		case *ag.ThinkingEndEvent:
			kinds = append(kinds, "thinking_end")
		case *ag.ToolCallStartEvent:
			kinds = append(kinds, "toolcall_start")
		case *ag.ToolCallDeltaEvent:
			kinds = append(kinds, "toolcall_delta")
		case *ag.ToolCallEndEvent:
			kinds = append(kinds, "toolcall_end")
		case *ag.DoneEvent:
			kinds = append(kinds, "done")
		case *ag.ErrorEvent:
			kinds = append(kinds, "error")
		}
	}
	return kinds
}

func TestStreamSimple_CannedSSE(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("unexpected auth header: %q", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(cannedSSE))
	}))
	defer server.Close()

	model := newTestModel(server.URL)
	c := &ag.Context{
		SystemPrompt: "be helpful",
		Messages:     []ag.Message{ag.NewUserText("weather?")},
	}
	opts := &ag.StreamOptions{APIKey: "test-key", MaxTokens: 256}

	stream := StreamSimple(context.Background(), model, c, opts)
	if stream == nil {
		t.Fatal("stream is nil")
	}
	events := drain(stream)

	kinds := eventKinds(events)
	want := []string{
		"start",
		"text_start", "text_delta", "text_delta",
		"toolcall_start", "toolcall_delta", "toolcall_delta",
		"text_end", "toolcall_end",
		"done",
	}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("event order mismatch:\n got: %v\nwant: %v", kinds, want)
	}

	msg := stream.Result()
	if msg == nil {
		t.Fatal("Result() returned nil")
	}

	if msg.StopReason != ag.StopReasonToolUse {
		t.Errorf("stop reason = %q, want toolUse", msg.StopReason)
	}

	// Concatenated text.
	var text string
	var toolCall *ag.ToolCall
	for _, block := range msg.Content {
		switch b := block.(type) {
		case *ag.TextContent:
			text = b.Text
		case *ag.ToolCall:
			toolCall = b
		}
	}
	if text != "Hello, world" {
		t.Errorf("text = %q, want %q", text, "Hello, world")
	}
	if toolCall == nil {
		t.Fatal("no tool call in result")
	}
	if toolCall.ID != "call_1" || toolCall.Name != "get_weather" {
		t.Errorf("tool call id/name = %q/%q", toolCall.ID, toolCall.Name)
	}
	wantArgs := map[string]any{"location": "SF"}
	if !reflect.DeepEqual(toolCall.Arguments, wantArgs) {
		t.Errorf("tool call args = %v, want %v", toolCall.Arguments, wantArgs)
	}

	// Usage: input = 100 - 40 cacheRead - 0 cacheWrite = 60.
	if msg.Usage.Input != 60 {
		t.Errorf("usage.Input = %d, want 60", msg.Usage.Input)
	}
	if msg.Usage.Output != 20 {
		t.Errorf("usage.Output = %d, want 20", msg.Usage.Output)
	}
	if msg.Usage.CacheRead != 40 {
		t.Errorf("usage.CacheRead = %d, want 40", msg.Usage.CacheRead)
	}
	if msg.Usage.TotalTokens != 120 {
		t.Errorf("usage.TotalTokens = %d, want 120", msg.Usage.TotalTokens)
	}
	// Cost: input 60/1e6*1 + output 20/1e6*2 + cacheRead 40/1e6*0.5
	wantCost := 60.0/1e6*1 + 20.0/1e6*2 + 40.0/1e6*0.5
	if msg.Usage.Cost.Total != wantCost {
		t.Errorf("usage.Cost.Total = %v, want %v", msg.Usage.Cost.Total, wantCost)
	}
	if msg.ResponseID != "chatcmpl-1" {
		t.Errorf("response id = %q", msg.ResponseID)
	}
}

func TestStreamSimple_Non2xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad key"}`))
	}))
	defer server.Close()

	model := newTestModel(server.URL)
	c := &ag.Context{Messages: []ag.Message{ag.NewUserText("hi")}}
	opts := &ag.StreamOptions{APIKey: "test-key"}

	stream := StreamSimple(context.Background(), model, c, opts)
	if stream == nil {
		t.Fatal("stream is nil")
	}
	events := drain(stream)
	kinds := eventKinds(events)
	want := []string{"start", "error"}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("event order = %v, want %v", kinds, want)
	}

	msg := stream.Result()
	if msg == nil {
		t.Fatal("Result() nil")
	}
	if msg.StopReason != ag.StopReasonError {
		t.Errorf("stop reason = %q, want error", msg.StopReason)
	}
	if msg.ErrorMessage == "" {
		t.Error("expected non-empty error message")
	}
}

func TestStreamSimple_Aborted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(cannedSSE))
	}))
	defer server.Close()

	model := newTestModel(server.URL)
	c := &ag.Context{Messages: []ag.Message{ag.NewUserText("hi")}}
	opts := &ag.StreamOptions{APIKey: "k"}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	stream := StreamSimple(ctx, model, c, opts)
	events := drain(stream)
	kinds := eventKinds(events)
	if len(kinds) == 0 || kinds[len(kinds)-1] != "error" {
		t.Fatalf("expected terminal error event, got %v", kinds)
	}
	msg := stream.Result()
	if msg.StopReason != ag.StopReasonAborted {
		t.Errorf("stop reason = %q, want aborted", msg.StopReason)
	}
}
