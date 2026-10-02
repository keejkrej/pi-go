package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	ag "github.com/keejkrej/pi-go/legacy/agentloop"
)

// bodyJSON round-trips a built request body through encoding/json so dynamic
// keys (wireMessage.Extra) are visible.
func bodyJSON(t *testing.T, model *ag.Model, c *ag.Context, opts *ag.StreamOptions) map[string]any {
	t.Helper()
	raw, err := json.Marshal(buildRequestBody(model, c, opts))
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	return out
}

func simpleContext() *ag.Context {
	return &ag.Context{Messages: []ag.Message{ag.NewUserText("hi")}}
}

func TestBuildRequestBody_ThinkingFormats(t *testing.T) {
	cases := []struct {
		name      string
		compat    *ag.Compat
		reasoning ag.ThinkingLevel
		levelMap  map[string]string
		modelOn   bool
		wantKey   string
		want      any
		absentKey string
	}{
		{
			name: "openai on", modelOn: true, reasoning: ag.ThinkingHigh,
			wantKey: "reasoning_effort", want: "high",
		},
		{
			name: "openai mapped", modelOn: true, reasoning: ag.ThinkingHigh,
			levelMap: map[string]string{"high": "max"},
			wantKey:  "reasoning_effort", want: "max",
		},
		{
			name: "openai off with off value", modelOn: true, reasoning: ag.ThinkingOff,
			levelMap: map[string]string{"off": "none"},
			wantKey:  "reasoning_effort", want: "none",
		},
		{
			name: "openai off", modelOn: true, reasoning: ag.ThinkingOff,
			absentKey: "reasoning_effort",
		},
		{
			name: "non reasoning model", modelOn: false, reasoning: ag.ThinkingHigh,
			absentKey: "reasoning_effort",
		},
		{
			name:   "zai on",
			compat: &ag.Compat{ThinkingFormat: ag.ThinkingFormatZai}, modelOn: true, reasoning: ag.ThinkingLow,
			wantKey: "thinking", want: map[string]any{"type": "enabled"},
		},
		{
			name:   "zai off",
			compat: &ag.Compat{ThinkingFormat: ag.ThinkingFormatZai}, modelOn: true, reasoning: ag.ThinkingOff,
			wantKey: "thinking", want: map[string]any{"type": "disabled"},
		},
		{
			name:   "qwen",
			compat: &ag.Compat{ThinkingFormat: ag.ThinkingFormatQwen}, modelOn: true, reasoning: ag.ThinkingOff,
			wantKey: "enable_thinking", want: false,
		},
		{
			name:   "qwen chat template",
			compat: &ag.Compat{ThinkingFormat: ag.ThinkingFormatQwenChatTemplate}, modelOn: true, reasoning: ag.ThinkingMedium,
			wantKey: "chat_template_kwargs",
			want:    map[string]any{"enable_thinking": true, "preserve_thinking": true},
		},
		{
			name: "chat template vars",
			compat: &ag.Compat{
				ThinkingFormat: ag.ThinkingFormatChatTemplate,
				ChatTemplateKwargs: map[string]any{
					"enable_thinking": ag.ChatTemplateVar{Var: "thinking.enabled"},
					"effort":          ag.ChatTemplateVar{Var: "thinking.effort", OmitWhenOff: true},
					"fixed":           "constant",
				},
			},
			modelOn: true, reasoning: ag.ThinkingOff,
			wantKey: "chat_template_kwargs",
			want:    map[string]any{"enable_thinking": false, "fixed": "constant"},
		},
		{
			name:   "openrouter",
			compat: &ag.Compat{ThinkingFormat: ag.ThinkingFormatOpenRouter}, modelOn: true, reasoning: ag.ThinkingHigh,
			wantKey: "reasoning", want: map[string]any{"effort": "high"},
		},
		{
			name:   "together",
			compat: &ag.Compat{ThinkingFormat: ag.ThinkingFormatTogether}, modelOn: true, reasoning: ag.ThinkingOff,
			wantKey: "reasoning", want: map[string]any{"enabled": false},
		},
		{
			name:   "string thinking",
			compat: &ag.Compat{ThinkingFormat: ag.ThinkingFormatStringThinking}, modelOn: true, reasoning: ag.ThinkingLow,
			wantKey: "thinking", want: "low",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			model := newTestModel("http://unused")
			model.Reasoning = tc.modelOn
			model.ThinkingLevelMap = tc.levelMap
			model.Compat = tc.compat
			body := bodyJSON(t, model, simpleContext(), &ag.StreamOptions{Reasoning: tc.reasoning})
			if tc.wantKey != "" {
				got, ok := body[tc.wantKey]
				if !ok {
					t.Fatalf("missing key %q in body: %v", tc.wantKey, body)
				}
				if !reflect.DeepEqual(got, tc.want) {
					t.Fatalf("body[%q] = %#v, want %#v", tc.wantKey, got, tc.want)
				}
			}
			if tc.absentKey != "" {
				if _, ok := body[tc.absentKey]; ok {
					t.Fatalf("key %q should be absent, body: %v", tc.absentKey, body)
				}
			}
		})
	}
}

func TestBuildRequestBody_ToolChoice(t *testing.T) {
	model := newTestModel("http://unused")

	body := bodyJSON(t, model, simpleContext(), &ag.StreamOptions{ToolChoice: &ag.ToolChoice{Mode: "required"}})
	if body["tool_choice"] != "required" {
		t.Fatalf("tool_choice = %v, want required", body["tool_choice"])
	}

	body = bodyJSON(t, model, simpleContext(), &ag.StreamOptions{ToolChoice: &ag.ToolChoice{Mode: "function", Name: "finish"}})
	want := map[string]any{"type": "function", "function": map[string]any{"name": "finish"}}
	if !reflect.DeepEqual(body["tool_choice"], want) {
		t.Fatalf("tool_choice = %#v, want %#v", body["tool_choice"], want)
	}

	body = bodyJSON(t, model, simpleContext(), &ag.StreamOptions{})
	if _, ok := body["tool_choice"]; ok {
		t.Fatal("tool_choice should be absent by default")
	}
}

func TestBuildRequestBody_ExtraBodyAndOnPayload(t *testing.T) {
	model := newTestModel("http://unused")
	model.Compat = &ag.Compat{ExtraBody: map[string]any{"custom_field": "v1"}}

	opts := &ag.StreamOptions{
		OnPayload: func(body map[string]any, m *ag.Model) map[string]any {
			body["hooked"] = true
			return body
		},
	}
	body := bodyJSON(t, model, simpleContext(), opts)
	if body["custom_field"] != "v1" {
		t.Fatalf("custom_field = %v", body["custom_field"])
	}
	if body["hooked"] != true {
		t.Fatalf("hooked = %v", body["hooked"])
	}
}

func TestBuildRequestBody_CompatFlags(t *testing.T) {
	model := newTestModel("http://unused")
	model.Reasoning = true
	model.Compat = &ag.Compat{
		NoUsageInStreaming:    true,
		SupportsDeveloperRole: true,
		SupportsStrictMode:    true,
	}
	c := &ag.Context{
		SystemPrompt: "sys",
		Messages:     []ag.Message{ag.NewUserText("hi")},
		Tools: []ag.AgentTool{&ag.FuncTool{
			NameVal:       "t",
			ParametersVal: map[string]any{"type": "object"},
		}},
	}
	body := bodyJSON(t, model, c, &ag.StreamOptions{})
	if _, ok := body["stream_options"]; ok {
		t.Fatal("stream_options should be omitted with NoUsageInStreaming")
	}
	msgs := body["messages"].([]any)
	first := msgs[0].(map[string]any)
	if first["role"] != "developer" {
		t.Fatalf("system role = %v, want developer", first["role"])
	}
	tools := body["tools"].([]any)
	fn := tools[0].(map[string]any)["function"].(map[string]any)
	if strict, ok := fn["strict"]; !ok || strict != false {
		t.Fatalf("strict = %v (present=%v), want false", strict, ok)
	}
}

func TestConvertMessages_ThinkingReplay(t *testing.T) {
	model := newTestModel("http://unused")
	assistant := &ag.AssistantMessage{
		Content: []ag.Content{
			&ag.ThinkingContent{Thinking: "pondering...", ThinkingSignature: "reasoning_content"},
			&ag.TextContent{Text: "answer"},
		},
		StopReason: ag.StopReasonStop,
	}
	c := &ag.Context{Messages: []ag.Message{ag.NewUserText("q"), assistant}}

	body := bodyJSON(t, model, c, &ag.StreamOptions{})
	msgs := body["messages"].([]any)
	last := msgs[len(msgs)-1].(map[string]any)
	if last["reasoning_content"] != "pondering..." {
		t.Fatalf("reasoning_content = %v, want replayed thinking", last["reasoning_content"])
	}
	if last["content"] != "answer" {
		t.Fatalf("content = %v", last["content"])
	}

	// RequiresThinkingAsText folds thinking into the text instead.
	model.Compat = &ag.Compat{RequiresThinkingAsText: true}
	body = bodyJSON(t, model, c, &ag.StreamOptions{})
	msgs = body["messages"].([]any)
	last = msgs[len(msgs)-1].(map[string]any)
	if _, ok := last["reasoning_content"]; ok {
		t.Fatal("reasoning_content should be absent with RequiresThinkingAsText")
	}
	if last["content"] != "pondering...answer" {
		t.Fatalf("content = %v, want thinking prepended as text", last["content"])
	}
}

func TestConvertMessages_ToolResultImages(t *testing.T) {
	assistant := &ag.AssistantMessage{
		Content:    []ag.Content{&ag.ToolCall{ID: "call_1", Name: "screenshot"}},
		StopReason: ag.StopReasonToolUse,
	}
	toolRes := &ag.ToolResultMessage{
		ToolCallID: "call_1",
		ToolName:   "screenshot",
		Content: []ag.Content{
			&ag.TextContent{Text: "captured"},
			&ag.ImageContent{Data: "AAAA", MimeType: "image/png"},
		},
	}
	c := &ag.Context{Messages: []ag.Message{ag.NewUserText("q"), assistant, toolRes}}

	// Vision model: image re-emitted as a trailing user message.
	model := newTestModel("http://unused")
	model.Input = []string{"text", "image"}
	body := bodyJSON(t, model, c, &ag.StreamOptions{})
	msgs := body["messages"].([]any)
	last := msgs[len(msgs)-1].(map[string]any)
	if last["role"] != "user" {
		t.Fatalf("last message role = %v, want user image carrier", last["role"])
	}
	parts := last["content"].([]any)
	if len(parts) != 2 {
		t.Fatalf("expected text preamble + image part, got %d parts", len(parts))
	}
	img := parts[1].(map[string]any)
	if img["type"] != "image_url" {
		t.Fatalf("part type = %v", img["type"])
	}
	url := img["image_url"].(map[string]any)["url"].(string)
	if url != "data:image/png;base64,AAAA" {
		t.Fatalf("image url = %q", url)
	}

	// Text-only model: no trailing user message.
	model = newTestModel("http://unused")
	body = bodyJSON(t, model, c, &ag.StreamOptions{})
	msgs = body["messages"].([]any)
	last = msgs[len(msgs)-1].(map[string]any)
	if last["role"] != "tool" {
		t.Fatalf("last message role = %v, want tool (no image carrier)", last["role"])
	}
}

func TestConvertMessages_ToolResultCompatFlags(t *testing.T) {
	assistant := &ag.AssistantMessage{
		Content:    []ag.Content{&ag.ToolCall{ID: "call_1", Name: "lookup"}},
		StopReason: ag.StopReasonToolUse,
	}
	toolRes := &ag.ToolResultMessage{
		ToolCallID: "call_1", ToolName: "lookup",
		Content: []ag.Content{&ag.TextContent{Text: "ok"}},
	}
	c := &ag.Context{Messages: []ag.Message{
		ag.NewUserText("q"), assistant, toolRes, ag.NewUserText("next"),
	}}

	model := newTestModel("http://unused")
	model.Compat = &ag.Compat{
		RequiresToolResultName:           true,
		RequiresAssistantAfterToolResult: true,
	}
	body := bodyJSON(t, model, c, &ag.StreamOptions{})
	msgs := body["messages"].([]any)

	var toolMsg, synthetic map[string]any
	for i, m := range msgs {
		mm := m.(map[string]any)
		if mm["role"] == "tool" {
			toolMsg = mm
			if i+1 < len(msgs) {
				synthetic = msgs[i+1].(map[string]any)
			}
		}
	}
	if toolMsg == nil {
		t.Fatal("no tool message")
	}
	if toolMsg["name"] != "lookup" {
		t.Fatalf("tool message name = %v, want lookup", toolMsg["name"])
	}
	if synthetic == nil || synthetic["role"] != "assistant" {
		t.Fatalf("expected synthetic assistant after tool result, got %v", synthetic)
	}
}

func TestStreamSimple_PreStreamRetry(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"slow down"}`))
			return
		}
		if got := r.Header.Get("X-Extra"); got != "yes" {
			t.Errorf("per-request header X-Extra = %q, want yes", got)
		}
		if got := r.Header.Get("X-Model-Default"); got != "" {
			t.Errorf("suppressed model header still present: %q", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(cannedSSE))
	}))
	defer server.Close()

	model := newTestModel(server.URL)
	model.Headers = map[string]string{"X-Model-Default": "on"}
	c := &ag.Context{Messages: []ag.Message{ag.NewUserText("hi")}}
	opts := &ag.StreamOptions{
		APIKey:        "k",
		MaxRetries:    2,
		MaxRetryDelay: 10 * time.Millisecond, // caps the Retry-After second
		Headers:       map[string]string{"X-Extra": "yes", "X-Model-Default": ""},
	}

	stream := StreamSimple(context.Background(), model, c, opts)
	drain(stream)
	msg := stream.Result()
	if msg.StopReason != ag.StopReasonToolUse {
		t.Fatalf("stop reason = %q (err=%q), want toolUse after retry", msg.StopReason, msg.ErrorMessage)
	}
	if calls.Load() != 2 {
		t.Fatalf("server calls = %d, want 2", calls.Load())
	}
}

func TestStreamSimple_NoRetryByDefault(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	model := newTestModel(server.URL)
	c := &ag.Context{Messages: []ag.Message{ag.NewUserText("hi")}}
	stream := StreamSimple(context.Background(), model, c, &ag.StreamOptions{APIKey: "k"})
	drain(stream)
	if msg := stream.Result(); msg.StopReason != ag.StopReasonError {
		t.Fatalf("stop reason = %q, want error", msg.StopReason)
	}
	if calls.Load() != 1 {
		t.Fatalf("server calls = %d, want 1 (no retries by default)", calls.Load())
	}
}
