// Ported from packages/agent/test/proxy.test.ts (pi v1.5.2).

package agent_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

func tproxyTestModel() *ai.Model {
	return &ai.Model{
		BaseModel: ai.BaseModel{
			Id:       "claude-3-5-sonnet-latest",
			Name:     "Claude 3.5 Sonnet",
			Api:      string(ai.KnownApiAnthropicMessages),
			Provider: ai.KnownProviderAnthropic,
			BaseUrl:  "https://api.anthropic.com",
			Input:    []ai.ModelModality{ai.ModelModalityText},
		},
		Reasoning:     true,
		ContextWindow: 200000,
		MaxTokens:     8192,
	}
}

func tproxyIntPtr(n int) *int           { return &n }
func tproxyInt64Ptr(n int64) *int64     { return &n }
func tproxyFloatPtr(f float64) *float64 { return &f }
func tproxyStrPtr(s string) *string     { return &s }

func TestProxy_ForwardsExpectedOptionsAndExcludesSensitiveFields(t *testing.T) {
	var capturedPath string
	var capturedAuth string
	var capturedContentType string
	var capturedBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedAuth = r.Header.Get("Authorization")
		capturedContentType = r.Header.Get("Content-Type")
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		capturedBody = b

		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"start\"}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"done\",\"reason\":\"stop\",\"usage\":{\"input\":1,\"output\":2,\"cacheRead\":0,\"cacheWrite\":0,\"totalTokens\":3,\"cost\":{\"input\":0,\"output\":0,\"cacheRead\":0,\"cacheWrite\":0,\"total\":0}}}\n\n")
	}))
	defer srv.Close()

	reasoning := ai.ThinkingLevelHigh
	cache := ai.CacheRetentionShort
	transport := ai.TransportSse
	opts := &agent.ProxyStreamOptions{
		SimpleStreamOptions: ai.SimpleStreamOptions{
			StreamOptions: ai.StreamOptions{
				ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
					ApiKey:          tproxyStrPtr("secret-api-key"),
					HTTPClient:      srv.Client(),
					MaxRetryDelayMs: tproxyInt64Ptr(5000),
				},
				Temperature:    tproxyFloatPtr(0.25),
				MaxTokens:      tproxyIntPtr(1024),
				CacheRetention: &cache,
				SessionId:      tproxyStrPtr("sess-123"),
				Transport:      &transport,
				Metadata:       jsonx.ObjectOf("user_id", "u1"),
			},
			Reasoning: &reasoning,
			ThinkingBudgets: &ai.ThinkingBudgets{
				High: tproxyIntPtr(4096),
			},
		},
		AuthToken: "proxy-token-xyz",
		ProxyUrl:  srv.URL,
	}

	reqOpts := agent.BuildProxyRequestOptions(opts)
	for _, excluded := range []string{"authToken", "proxyUrl", "apiKey", "signal"} {
		if _, ok := reqOpts.Get(excluded); ok {
			t.Fatalf("BuildProxyRequestOptions unexpectedly included %q", excluded)
		}
	}

	msg := &ai.UserMessage{
		Role:      "user",
		Content:   ai.UserMessageContent{Text: tproxyStrPtr("hi")},
		Timestamp: time.Now().UnixMilli(),
	}
	stream := agent.StreamProxy(t.Context(), tproxyTestModel(), &ai.TranscriptContext{Messages: ai.MessageList{msg}}, opts)
	res, err := stream.Result(t.Context())
	if err != nil {
		t.Fatalf("stream.Result: %v", err)
	}
	if res == nil || res.StopReason != ai.StopReasonStop {
		t.Fatalf("unexpected result: %+v", res)
	}

	if capturedPath != "/api/stream" {
		t.Fatalf("expected /api/stream, got %q", capturedPath)
	}
	if capturedAuth != "Bearer proxy-token-xyz" {
		t.Fatalf("expected Bearer auth, got %q", capturedAuth)
	}
	if capturedContentType != "application/json" {
		t.Fatalf("expected application/json, got %q", capturedContentType)
	}

	parsed, err := jsonx.Parse(capturedBody)
	if err != nil {
		t.Fatalf("parse captured body: %v", err)
	}
	bodyObj, ok := parsed.(*jsonx.Object)
	if !ok {
		t.Fatalf("expected body object, got %T", parsed)
	}
	optsRaw, _ := bodyObj.Get("options")
	optsObj, ok := optsRaw.(*jsonx.Object)
	if !ok {
		t.Fatalf("expected options object, got %T", optsRaw)
	}
	for _, expected := range []string{"temperature", "maxTokens", "reasoning", "thinkingBudgets", "cacheRetention", "sessionId", "transport", "maxRetryDelayMs", "metadata"} {
		if _, ok := optsObj.Get(expected); !ok {
			t.Fatalf("expected forwarded option %q to be present", expected)
		}
	}
	for _, excluded := range []string{"authToken", "proxyUrl", "apiKey", "signal"} {
		if _, ok := optsObj.Get(excluded); ok {
			t.Fatalf("expected option %q to be excluded", excluded)
		}
	}
}

func TestProxy_ReconstructsContentBlocksAndUsage(t *testing.T) {
	sseBody := strings.Join([]string{
		`data: {"type":"start"}`,
		``,
		`data: {"type":"thinking_start","contentIndex":0}`,
		``,
		`data: {"type":"thinking_delta","contentIndex":0,"delta":"Let me "}`,
		``,
		`data: {"type":"thinking_delta","contentIndex":0,"delta":"think."}`,
		``,
		`data: {"type":"thinking_end","contentIndex":0,"contentSignature":"think-sig-1"}`,
		``,
		`data: {"type":"text_start","contentIndex":1}`,
		``,
		`data: {"type":"text_delta","contentIndex":1,"delta":"Hello "}`,
		``,
		`data: {"type":"text_delta","contentIndex":1,"delta":"world"}`,
		``,
		`data: {"type":"text_end","contentIndex":1,"contentSignature":"text-sig-2"}`,
		``,
		`data: {"type":"toolcall_start","contentIndex":2,"id":"call_1","toolName":"calculate"}`,
		``,
		`data: {"type":"toolcall_delta","contentIndex":2,"delta":"{\"express"}`,
		``,
		`data: {"type":"toolcall_delta","contentIndex":2,"delta":"ion\":\"2+2\"}"}`,
		``,
		`data: {"type":"toolcall_end","contentIndex":2}`,
		``,
		`data: {"type":"done","reason":"toolUse","usage":{"input":10,"output":25,"cacheRead":2,"cacheWrite":1,"totalTokens":38,"cost":{"input":0.01,"output":0.02,"cacheRead":0,"cacheWrite":0,"total":0.03}}}`,
		``,
	}, "\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, sseBody)
	}))
	defer srv.Close()

	opts := &agent.ProxyStreamOptions{
		SimpleStreamOptions: ai.SimpleStreamOptions{
			StreamOptions: ai.StreamOptions{
				ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
					HTTPClient: srv.Client(),
				},
			},
		},
		AuthToken: "tok",
		ProxyUrl:  srv.URL,
	}

	stream := agent.StreamProxy(t.Context(), tproxyTestModel(), &ai.TranscriptContext{Messages: ai.MessageList{}}, opts)
	var events []ai.AssistantMessageEvent
	for ev := range stream.All() {
		events = append(events, ev)
	}
	res, err := stream.Result(t.Context())
	if err != nil {
		t.Fatalf("stream.Result: %v", err)
	}

	if len(events) != 14 {
		t.Fatalf("expected 14 events, got %d", len(events))
	}
	if res.StopReason != ai.StopReasonToolUse {
		t.Fatalf("expected stopReason toolUse, got %q", res.StopReason)
	}
	if res.Usage.Input != 10 || res.Usage.Output != 25 || res.Usage.TotalTokens != 38 {
		t.Fatalf("unexpected usage: %+v", res.Usage)
	}
	if len(res.Content) != 3 {
		t.Fatalf("expected 3 content blocks, got %d", len(res.Content))
	}

	thinking, ok := res.Content[0].(*ai.ThinkingContent)
	if !ok || thinking.Thinking != "Let me think." || thinking.ThinkingSignature == nil || *thinking.ThinkingSignature != "think-sig-1" {
		t.Fatalf("unexpected thinking block: %#v", res.Content[0])
	}
	text, ok := res.Content[1].(*ai.TextContent)
	if !ok || text.Text != "Hello world" || text.TextSignature == nil || *text.TextSignature != "text-sig-2" {
		t.Fatalf("unexpected text block: %#v", res.Content[1])
	}
	tc, ok := res.Content[2].(*ai.ToolCall)
	if !ok || tc.Id != "call_1" || tc.Name != "calculate" || tc.Arguments == nil {
		t.Fatalf("unexpected toolCall block: %#v", res.Content[2])
	}
	if expr, ok := tc.Arguments.Get("expression"); !ok || expr != "2+2" {
		t.Fatalf("unexpected toolCall arguments: %v", tc.Arguments)
	}
}

func TestProxy_ProcessesTerminalMetadataWithoutTrailingNewline(t *testing.T) {
	payload := strings.Join([]string{
		`data: {"type":"start"}`,
		``,
		`data: {"type":"text_start","contentIndex":0}`,
		``,
		`data: {"type":"text_delta","contentIndex":0,"delta":"ok"}`,
		``,
		`data: {"type":"text_end","contentIndex":0}`,
		``,
		// Intentionally omit trailing newline / blank line at EOF.
		`data: {"type":"done","reason":"stop","providerThinkingLevel":"high","usage":{"input":5,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":6,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}}`,
	}, "\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, payload)
	}))
	defer srv.Close()

	opts := &agent.ProxyStreamOptions{
		SimpleStreamOptions: ai.SimpleStreamOptions{
			StreamOptions: ai.StreamOptions{
				ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
					HTTPClient: srv.Client(),
				},
			},
		},
		AuthToken: "tok",
		ProxyUrl:  srv.URL,
	}

	stream := agent.StreamProxy(t.Context(), tproxyTestModel(), &ai.TranscriptContext{Messages: ai.MessageList{}}, opts)
	res, err := stream.Result(t.Context())
	if err != nil {
		t.Fatalf("stream.Result: %v", err)
	}
	if res.StopReason != ai.StopReasonStop || res.Usage.TotalTokens != 6 {
		t.Fatalf("expected done metadata to be parsed at EOF without trailing newline, got %+v", res)
	}
	if res.ProviderThinkingLevel == nil || *res.ProviderThinkingLevel != "high" {
		t.Fatalf("expected providerThinkingLevel high preserved, got %v", res.ProviderThinkingLevel)
	}
}

func TestProxy_HandlesHttpErrorsAndAbortSignal(t *testing.T) {
	t.Run("http error response maps to AssistantMessageEventError", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":"rate limited"}`))
		}))
		defer srv.Close()

		opts := &agent.ProxyStreamOptions{
			SimpleStreamOptions: ai.SimpleStreamOptions{
				StreamOptions: ai.StreamOptions{
					ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
						HTTPClient: srv.Client(),
					},
				},
			},
			AuthToken: "tok",
			ProxyUrl:  srv.URL,
		}
		stream := agent.StreamProxy(t.Context(), tproxyTestModel(), &ai.TranscriptContext{Messages: ai.MessageList{}}, opts)
		res, err := stream.Result(t.Context())
		if err != nil {
			t.Fatalf("stream.Result: %v", err)
		}
		if res.StopReason != ai.StopReasonError {
			t.Fatalf("expected stopReason error, got %q", res.StopReason)
		}
		if res.ErrorMessage == nil || !strings.Contains(*res.ErrorMessage, "rate limited") {
			t.Fatalf("expected rate limited error message, got %v", res.ErrorMessage)
		}
	})

	t.Run("context cancellation maps to StopReasonAborted", func(t *testing.T) {
		started := make(chan struct{})
		release := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			fl, _ := w.(http.Flusher)
			io.WriteString(w, "data: {\"type\":\"start\"}\n\n")
			if fl != nil {
				fl.Flush()
			}
			close(started)
			select {
			case <-r.Context().Done():
			case <-release:
			}
		}))
		defer close(release)
		defer srv.Close()

		ctx, cancel := context.WithCancel(t.Context())
		opts := &agent.ProxyStreamOptions{
			SimpleStreamOptions: ai.SimpleStreamOptions{
				StreamOptions: ai.StreamOptions{
					ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
						HTTPClient: srv.Client(),
					},
				},
			},
			AuthToken: "tok",
			ProxyUrl:  srv.URL,
		}
		stream := agent.StreamProxy(ctx, tproxyTestModel(), &ai.TranscriptContext{Messages: ai.MessageList{}}, opts)
		<-started
		cancel()

		var lastEv ai.AssistantMessageEvent
		for ev := range stream.All() {
			lastEv = ev
		}
		errEv, ok := lastEv.(*ai.AssistantMessageEventError)
		if !ok {
			t.Fatalf("expected terminal error event on cancel, got %T", lastEv)
		}
		if errEv.Reason != ai.StopReasonAborted || errEv.Error.StopReason != ai.StopReasonAborted {
			t.Fatalf("expected StopReasonAborted, got reason=%q msg.StopReason=%q", errEv.Reason, errEv.Error.StopReason)
		}
	})

	t.Run("non-JSON http status text does not duplicate status code", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			io.WriteString(w, "plain text failure")
		}))
		defer srv.Close()

		opts := &agent.ProxyStreamOptions{
			SimpleStreamOptions: ai.SimpleStreamOptions{
				StreamOptions: ai.StreamOptions{
					ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
						HTTPClient: srv.Client(),
					},
				},
			},
			AuthToken: "tok",
			ProxyUrl:  srv.URL,
		}
		stream := agent.StreamProxy(t.Context(), tproxyTestModel(), &ai.TranscriptContext{Messages: ai.MessageList{}}, opts)
		res, err := stream.Result(t.Context())
		if err != nil {
			t.Fatalf("stream.Result: %v", err)
		}
		if res.ErrorMessage == nil || *res.ErrorMessage != "Proxy error: 500 Internal Server Error" {
			t.Fatalf("expected 'Proxy error: 500 Internal Server Error', got %v", res.ErrorMessage)
		}
	})
}

func TestProxy_PreservesToolCallMetadataOnToolcallEnd(t *testing.T) {
	sseBody := strings.Join([]string{
		`data: {"type":"start"}`,
		`data: {"type":"toolcall_start","contentIndex":0,"id":"call_test|fc_test","toolName":"lookup"}`,
		`data: {"type":"toolcall_delta","contentIndex":0,"delta":"{\"value\":\"hello\"}"}`,
		`data: {"type":"toolcall_end","contentIndex":0,"toolCall":{"type":"toolCall","id":"call_test|fc_test","name":"lookup","arguments":{"value":"hello"},"namespace":"dynamic_tools"}}`,
		`data: {"type":"done","reason":"toolUse","usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":2,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}}`,
		``,
	}, "\n\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, sseBody)
	}))
	defer srv.Close()

	opts := &agent.ProxyStreamOptions{
		SimpleStreamOptions: ai.SimpleStreamOptions{
			StreamOptions: ai.StreamOptions{
				ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
					HTTPClient: srv.Client(),
				},
			},
		},
		AuthToken: "test-token",
		ProxyUrl:  srv.URL,
	}

	stream := agent.StreamProxy(t.Context(), tproxyTestModel(), &ai.TranscriptContext{Messages: ai.MessageList{}}, opts)
	var endEv *ai.AssistantMessageEventToolcallEnd
	for ev := range stream.All() {
		if e, ok := ev.(*ai.AssistantMessageEventToolcallEnd); ok {
			endEv = e
		}
	}
	res, err := stream.Result(t.Context())
	if err != nil {
		t.Fatalf("stream.Result: %v", err)
	}
	if endEv == nil || endEv.ToolCall == nil || endEv.ToolCall.Namespace == nil || *endEv.ToolCall.Namespace != "dynamic_tools" {
		t.Fatalf("expected toolcall_end event namespace 'dynamic_tools', got %+v", endEv)
	}
	tc, ok := res.Content[0].(*ai.ToolCall)
	if !ok || tc.Namespace == nil || *tc.Namespace != "dynamic_tools" {
		t.Fatalf("expected result ToolCall namespace 'dynamic_tools', got %+v", res.Content[0])
	}
}

func TestProxy_EmitsErrorWhenStreamEndsWithoutTerminalEventAndIgnoresUnknown(t *testing.T) {
	sseBody := "data: {\"type\":\"start\"}\n\ndata: {\"type\":\"future_ignored_event\",\"x\":1}\n\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, sseBody)
	}))
	defer srv.Close()

	opts := &agent.ProxyStreamOptions{
		SimpleStreamOptions: ai.SimpleStreamOptions{
			StreamOptions: ai.StreamOptions{
				ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
					HTTPClient: srv.Client(),
				},
			},
		},
		AuthToken: "test-token",
		ProxyUrl:  srv.URL,
	}

	stream := agent.StreamProxy(t.Context(), tproxyTestModel(), &ai.TranscriptContext{Messages: ai.MessageList{}}, opts)
	var typesSeen []string
	for ev := range stream.All() {
		switch ev.(type) {
		case *ai.AssistantMessageEventStart:
			typesSeen = append(typesSeen, "start")
		case *ai.AssistantMessageEventError:
			typesSeen = append(typesSeen, "error")
		default:
			typesSeen = append(typesSeen, "other")
		}
	}
	res, err := stream.Result(t.Context())
	if err != nil {
		t.Fatalf("stream.Result: %v", err)
	}
	if len(typesSeen) != 2 || typesSeen[0] != "start" || typesSeen[1] != "error" {
		t.Fatalf("expected ['start', 'error'], got %v", typesSeen)
	}
	if res.StopReason != ai.StopReasonError || res.ErrorMessage == nil || !strings.Contains(*res.ErrorMessage, "Connection closed by proxy server") {
		t.Fatalf("expected closed connection error, got %+v", res)
	}
}

func TestProxy_ShowsGrowingPartialArgumentsLinearTime(t *testing.T) {
	content := strings.Repeat("line \"quoted\" \\ end\n", 20000)
	fullArgs := jsonx.ObjectOf("path", "a.ts", "content", content)
	argsJSON, err := jsonx.Marshal(fullArgs)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	text := string(argsJSON)

	var sb strings.Builder
	sb.WriteString("data: {\"type\":\"start\"}\n\n")
	sb.WriteString("data: {\"type\":\"toolcall_start\",\"contentIndex\":0,\"id\":\"call_1\",\"toolName\":\"write\"}\n\n")
	for idx := 0; idx < len(text); idx += 32 {
		end := idx + 32
		if end > len(text) {
			end = len(text)
		}
		deltaEv := jsonx.ObjectOf("type", "toolcall_delta", "contentIndex", 0, "delta", text[idx:end])
		b, _ := jsonx.Marshal(deltaEv)
		sb.WriteString("data: ")
		sb.Write(b)
		sb.WriteString("\n\n")
	}
	endEv := jsonx.ObjectOf(
		"type", "toolcall_end",
		"contentIndex", 0,
		"toolCall", jsonx.ObjectOf("type", "toolCall", "id", "call_1", "name", "write", "arguments", fullArgs),
	)
	eb, _ := jsonx.Marshal(endEv)
	sb.WriteString("data: ")
	sb.Write(eb)
	sb.WriteString("\n\n")
	sb.WriteString("data: {\"type\":\"done\",\"reason\":\"toolUse\",\"usage\":{\"input\":1,\"output\":1,\"cacheRead\":0,\"cacheWrite\":0,\"totalTokens\":2,\"cost\":{\"input\":0,\"output\":0,\"cacheRead\":0,\"cacheWrite\":0,\"total\":0}}}\n\n")
	body := sb.String()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, body)
	}))
	defer srv.Close()

	opts := &agent.ProxyStreamOptions{
		SimpleStreamOptions: ai.SimpleStreamOptions{
			StreamOptions: ai.StreamOptions{
				ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
					HTTPClient: srv.Client(),
				},
			},
		},
		AuthToken: "test-token",
		ProxyUrl:  srv.URL,
	}

	started := time.Now()
	stream := agent.StreamProxy(t.Context(), tproxyTestModel(), &ai.TranscriptContext{Messages: ai.MessageList{}}, opts)
	shown := 0
	deltas := 0
	for ev := range stream.All() {
		td, ok := ev.(*ai.AssistantMessageEventToolcallDelta)
		if !ok {
			continue
		}
		tc, ok := td.Partial.Content[0].(*ai.ToolCall)
		if !ok || tc.Arguments == nil {
			continue
		}
		partialVal, hasContent := tc.Arguments.Get("content")
		partial, ok := partialVal.(string)
		if !hasContent || !ok {
			continue
		}
		if deltas%500 == 0 && !strings.HasPrefix(content, partial) {
			t.Fatalf("expected partial content prefix at delta %d", deltas)
		}
		deltas++
		if len(partial) < shown {
			t.Fatalf("expected monotonic growing partial length: %d < %d", len(partial), shown)
		}
		shown = len(partial)
	}
	res, err := stream.Result(t.Context())
	if err != nil {
		t.Fatalf("stream.Result: %v", err)
	}
	if shown <= len(content)-64 {
		t.Fatalf("expected partial content near full length (%d), got %d", len(content), shown)
	}
	tc, ok := res.Content[0].(*ai.ToolCall)
	if !ok || tc.Id != "call_1" || tc.Name != "write" {
		t.Fatalf("unexpected final toolCall: %+v", res.Content[0])
	}
	if finalContent, _ := tc.Arguments.Get("content"); finalContent != content {
		t.Fatalf("expected full final content preserved")
	}
	if time.Since(started) >= 10*time.Second {
		t.Fatalf("took too long: %v", time.Since(started))
	}
}

func TestProxy_ForwardsExtendedOptionsAndHandlesNilOnPayload(t *testing.T) {
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"type\":\"start\"}\n\ndata: {\"type\":\"done\",\"reason\":\"stop\",\"usage\":{\"input\":0,\"output\":0,\"cacheRead\":0,\"cacheWrite\":0,\"totalTokens\":0,\"cost\":{\"input\":0,\"output\":0,\"cacheRead\":0,\"cacheWrite\":0,\"total\":0}}}\n\n")
	}))
	defer srv.Close()

	headers := omap.NewMap[string, *string]()
	hv := "v1"
	headers.Set("X-Custom", &hv)
	headers.Set("X-Suppress", nil)
	disCont := true
	opts := &agent.ProxyStreamOptions{
		SimpleStreamOptions: ai.SimpleStreamOptions{
			StreamOptions: ai.StreamOptions{
				ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
					HTTPClient: srv.Client(),
					Headers:    headers,
					OnPayload: func(_ any, _ *ai.Model) (any, error) {
						return nil, nil // nil leaves payload unchanged
					},
				},
				SamplingParams:      jsonx.ObjectOf("topK", 20),
				DisableContinuation: &disCont,
			},
		},
		AuthToken: "tok",
		ProxyUrl:  srv.URL,
	}

	stream := agent.StreamProxy(t.Context(), tproxyTestModel(), &ai.TranscriptContext{Messages: ai.MessageList{}}, opts)
	if _, err := stream.Result(t.Context()); err != nil {
		t.Fatalf("stream.Result: %v", err)
	}
	parsed, err := jsonx.Parse(capturedBody)
	if err != nil {
		t.Fatalf("parse captured body: %v", err)
	}
	bodyObj := parsed.(*jsonx.Object)
	optsRaw, _ := bodyObj.Get("options")
	optsObj := optsRaw.(*jsonx.Object)
	for _, k := range []string{"samplingParams", "disableContinuation", "headers"} {
		if _, ok := optsObj.Get(k); !ok {
			t.Fatalf("expected extended option %q in proxy payload", k)
		}
	}
}
