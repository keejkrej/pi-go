// Ported from packages/ai/test/faux-provider.test.ts (pi v1.5.2).

package providers_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

func tfPtr[T any](v T) *T { return &v }

func tfSetupFauxModels(t *testing.T, opts *providers.RegisterFauxProviderOptions) (*providers.FauxProviderHandle, ai.MutableModels) {
	t.Helper()
	h, err := providers.FauxProvider(opts)
	if err != nil {
		t.Fatalf("FauxProvider: %v", err)
	}
	models := ai.CreateModels(nil)
	models.SetProvider(h.Provider)
	return h, models
}

func tfCollectEvents(t *testing.T, stream *ai.AssistantMessageEventStream) []ai.AssistantMessageEvent {
	t.Helper()
	var events []ai.AssistantMessageEvent
	for ev := range stream.All() {
		events = append(events, ev)
	}
	return events
}

func tfEventTypes(events []ai.AssistantMessageEvent) []string {
	out := make([]string, len(events))
	for i, ev := range events {
		switch e := ev.(type) {
		case *ai.AssistantMessageEventStart:
			out[i] = e.Type
		case *ai.AssistantMessageEventTextStart:
			out[i] = e.Type
		case *ai.AssistantMessageEventTextDelta:
			out[i] = e.Type
		case *ai.AssistantMessageEventTextEnd:
			out[i] = e.Type
		case *ai.AssistantMessageEventThinkingStart:
			out[i] = e.Type
		case *ai.AssistantMessageEventThinkingDelta:
			out[i] = e.Type
		case *ai.AssistantMessageEventThinkingEnd:
			out[i] = e.Type
		case *ai.AssistantMessageEventToolcallStart:
			out[i] = e.Type
		case *ai.AssistantMessageEventToolcallDelta:
			out[i] = e.Type
		case *ai.AssistantMessageEventToolcallEnd:
			out[i] = e.Type
		case *ai.AssistantMessageEventDone:
			out[i] = e.Type
		case *ai.AssistantMessageEventError:
			out[i] = e.Type
		default:
			out[i] = "unknown"
		}
	}
	return out
}

func tfUserMsg(text string, ts int64) *ai.UserMessage {
	return &ai.UserMessage{
		Role:      "user",
		Content:   ai.UserMessageContent{Text: &text},
		Timestamp: ts,
	}
}

func TestFauxProvider_UsageAndDefaults(t *testing.T) {
	h, models := tfSetupFauxModels(t, nil)
	h.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("hello world"), nil)},
	})

	sysPrompt := "Be concise."
	resp, err := models.Complete(context.Background(), h.GetModel(), &ai.Context{
		SystemPrompt: &sysPrompt,
		Messages:     ai.MessageList{tfUserMsg("hi there", js.DateNow())},
	}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(resp.Content) != 1 {
		t.Fatalf("expected 1 block, got %d", len(resp.Content))
	}
	tc, ok := resp.Content[0].(*ai.TextContent)
	if !ok || tc.Text != "hello world" {
		t.Fatalf("unexpected content: %#v", resp.Content[0])
	}
	if resp.Usage.Input <= 0 || resp.Usage.Output <= 0 {
		t.Fatalf("expected positive usage, got %+v", resp.Usage)
	}
	if resp.Usage.TotalTokens != resp.Usage.Input+resp.Usage.Output {
		t.Fatalf("unexpected totalTokens: %+v", resp.Usage)
	}
	if h.State.CallCount != 1 {
		t.Fatalf("expected CallCount=1, got %d", h.State.CallCount)
	}
}

func TestFauxProvider_HelperBlocks(t *testing.T) {
	h, models := tfSetupFauxModels(t, nil)
	toolUse := ai.StopReasonToolUse
	h.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
				providers.FauxThinking("think"),
				providers.FauxToolCall("echo", jsonx.ObjectOf("text", "hi"), nil),
				providers.FauxText("done"),
			}, &providers.FauxAssistantMessageOptions{StopReason: &toolUse}),
		},
	})

	resp, err := models.Complete(context.Background(), h.GetModel(), &ai.Context{
		Messages: ai.MessageList{tfUserMsg("hi", js.DateNow())},
	}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.StopReason != ai.StopReasonToolUse {
		t.Fatalf("expected toolUse, got %q", resp.StopReason)
	}
	if len(resp.Content) != 3 {
		t.Fatalf("expected 3 content blocks, got %d", len(resp.Content))
	}
	if th, ok := resp.Content[0].(*ai.ThinkingContent); !ok || th.Thinking != "think" {
		t.Fatalf("unexpected block 0: %#v", resp.Content[0])
	}
	if tc, ok := resp.Content[1].(*ai.ToolCall); !ok || tc.Id == "" || tc.Name != "echo" || !tc.Arguments.Equal(jsonx.ObjectOf("text", "hi")) {
		t.Fatalf("unexpected block 1: %#v", resp.Content[1])
	}
	if tx, ok := resp.Content[2].(*ai.TextContent); !ok || tx.Text != "done" {
		t.Fatalf("unexpected block 2: %#v", resp.Content[2])
	}
}

func TestFauxProvider_MultipleModelsAndFactories(t *testing.T) {
	h, models := tfSetupFauxModels(t, &providers.RegisterFauxProviderOptions{
		Models: []providers.FauxModelDefinition{
			{Id: "faux-fast", Name: tfPtr("Faux Fast"), Reasoning: tfPtr(false)},
			{Id: "faux-thinker", Name: tfPtr("Faux Thinker"), Reasoning: tfPtr(true)},
		},
	})
	factory := providers.FauxResponseFactory(func(_ context.Context, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, model *ai.Model) (*ai.AssistantMessage, error) {
		flag := "false"
		if model.Reasoning {
			flag = "true"
		}
		return providers.FauxAssistantMessage(providers.FauxAssistantText(model.Id+":"+flag), nil), nil
	})
	h.SetResponses([]providers.FauxResponseStep{factory, factory})

	if h.GetModel() != h.Models[0] {
		t.Fatal("GetModel should return first model")
	}
	if h.GetModelId("faux-fast").Reasoning != false || h.GetModelId("faux-thinker").Reasoning != true {
		t.Fatal("per-model reasoning mismatch")
	}

	fast, err := models.Complete(context.Background(), h.GetModelId("faux-fast"), &ai.Context{
		Messages: ai.MessageList{tfUserMsg("hi", js.DateNow())},
	}, nil)
	if err != nil {
		t.Fatalf("Complete fast: %v", err)
	}
	thinker, err := models.Complete(context.Background(), h.GetModelId("faux-thinker"), &ai.Context{
		Messages: ai.MessageList{tfUserMsg("hi", js.DateNow())},
	}, nil)
	if err != nil {
		t.Fatalf("Complete thinker: %v", err)
	}

	if fast.Content[0].(*ai.TextContent).Text != "faux-fast:false" {
		t.Fatalf("unexpected fast response: %#v", fast.Content[0])
	}
	if thinker.Content[0].(*ai.TextContent).Text != "faux-thinker:true" {
		t.Fatalf("unexpected thinker response: %#v", thinker.Content[0])
	}
}

func TestFauxProvider_RewritesMetadata(t *testing.T) {
	h, models := tfSetupFauxModels(t, &providers.RegisterFauxProviderOptions{
		Api:      tfPtr("faux:test"),
		Provider: tfPtr("faux-provider"),
		Models:   []providers.FauxModelDefinition{{Id: "faux-model"}},
	})
	h.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("hello"), nil)},
	})

	resp, err := models.Complete(context.Background(), h.GetModel(), &ai.Context{
		Messages: ai.MessageList{tfUserMsg("hi", js.DateNow())},
	}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Api != "faux:test" || resp.Provider != "faux-provider" || resp.Model != "faux-model" {
		t.Fatalf("metadata mismatch: api=%q provider=%q model=%q", resp.Api, resp.Provider, resp.Model)
	}
}

func TestFauxProvider_QueueOrderAndExhaustion(t *testing.T) {
	h, models := tfSetupFauxModels(t, nil)
	h.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("first"), nil)},
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("second"), nil)},
	})

	ctxReq := &ai.Context{Messages: ai.MessageList{tfUserMsg("hi", js.DateNow())}}
	first, _ := models.Complete(context.Background(), h.GetModel(), ctxReq, nil)
	second, _ := models.Complete(context.Background(), h.GetModel(), ctxReq, nil)
	exhausted, _ := models.Complete(context.Background(), h.GetModel(), ctxReq, nil)

	if first.Content[0].(*ai.TextContent).Text != "first" || second.Content[0].(*ai.TextContent).Text != "second" {
		t.Fatalf("unexpected queue outputs: %#v / %#v", first.Content, second.Content)
	}
	if exhausted.StopReason != ai.StopReasonError || exhausted.ErrorMessage == nil || *exhausted.ErrorMessage != "No more faux responses queued" {
		t.Fatalf("unexpected exhausted response: %+v", exhausted)
	}
	if h.GetPendingResponseCount() != 0 || h.State.CallCount != 3 {
		t.Fatalf("count mismatch: pending=%d calls=%d", h.GetPendingResponseCount(), h.State.CallCount)
	}
}

func TestFauxProvider_SetAndAppendResponses(t *testing.T) {
	h, models := tfSetupFauxModels(t, nil)
	ctxReq := &ai.Context{Messages: ai.MessageList{tfUserMsg("hi", js.DateNow())}}

	h.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("first"), nil)},
	})
	r1, _ := models.Complete(context.Background(), h.GetModel(), ctxReq, nil)
	if r1.Content[0].(*ai.TextContent).Text != "first" || h.GetPendingResponseCount() != 0 {
		t.Fatalf("unexpected first response: %#v pending=%d", r1, h.GetPendingResponseCount())
	}

	h.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("second"), nil)},
	})
	if h.GetPendingResponseCount() != 1 {
		t.Fatalf("expected 1 pending, got %d", h.GetPendingResponseCount())
	}
	r2, _ := models.Complete(context.Background(), h.GetModel(), ctxReq, nil)
	if r2.Content[0].(*ai.TextContent).Text != "second" {
		t.Fatalf("unexpected second response: %#v", r2)
	}

	h.AppendResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("third"), nil)},
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("fourth"), nil)},
	})
	if h.GetPendingResponseCount() != 2 {
		t.Fatalf("expected 2 pending, got %d", h.GetPendingResponseCount())
	}
	r3, _ := models.Complete(context.Background(), h.GetModel(), ctxReq, nil)
	r4, _ := models.Complete(context.Background(), h.GetModel(), ctxReq, nil)
	if r3.Content[0].(*ai.TextContent).Text != "third" || r4.Content[0].(*ai.TextContent).Text != "fourth" {
		t.Fatalf("unexpected appended responses: %#v / %#v", r3.Content, r4.Content)
	}
	if h.GetPendingResponseCount() != 0 {
		t.Fatalf("expected 0 pending, got %d", h.GetPendingResponseCount())
	}
}

func TestFauxProvider_ResponseFactoryAndError(t *testing.T) {
	h, models := tfSetupFauxModels(t, nil)
	h.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseFactory(func(_ context.Context, transcript *ai.TranscriptContext, _ *ai.SimpleStreamOptions, state *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
			return providers.FauxAssistantMessage(providers.FauxAssistantText(string(rune('0'+len(transcript.Messages)))+":"+string(rune('0'+state.CallCount))), nil), nil
		}),
		providers.FauxResponseFactory(func(_ context.Context, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
			return nil, errors.New("boom")
		}),
	})

	r1, _ := models.Complete(context.Background(), h.GetModel(), &ai.Context{
		Messages: ai.MessageList{tfUserMsg("hi", js.DateNow())},
	}, nil)
	if r1.Content[0].(*ai.TextContent).Text != "1:1" {
		t.Fatalf("expected 1:1, got %#v", r1.Content)
	}

	events := tfCollectEvents(t, models.Stream(context.Background(), h.GetModel(), &ai.Context{
		Messages: ai.MessageList{tfUserMsg("hi", js.DateNow())},
	}, nil))
	if len(events) != 1 {
		t.Fatalf("expected 1 error event, got %d", len(events))
	}
	ev, ok := events[0].(*ai.AssistantMessageEventError)
	if !ok || ev.Error.StopReason != ai.StopReasonError || ev.Error.ErrorMessage == nil || *ev.Error.ErrorMessage != "boom" {
		t.Fatalf("unexpected error event: %#v", events[0])
	}
}

func TestFauxProvider_RejectsPendingStopReason(t *testing.T) {
	h, models := tfSetupFauxModels(t, nil)
	pending := ai.StopReasonPending
	h.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("partial"), &providers.FauxAssistantMessageOptions{StopReason: &pending})},
	})

	events := tfCollectEvents(t, models.Stream(context.Background(), h.GetModel(), &ai.Context{
		Messages: ai.MessageList{tfUserMsg("hi", js.DateNow())},
	}, nil))
	types := tfEventTypes(events)
	if slices.Contains(types, "done") {
		t.Fatalf("unexpected done event in %v", types)
	}
	term, ok := events[len(events)-1].(*ai.AssistantMessageEventError)
	if !ok || term.Error.StopReason != ai.StopReasonError || term.Error.ErrorMessage == nil || *term.Error.ErrorMessage != "Faux response ended without a stop reason" {
		t.Fatalf("unexpected terminal event: %#v", events[len(events)-1])
	}
}

func TestFauxProvider_PromptAndOutputTokenEstimate(t *testing.T) {
	h, models := tfSetupFauxModels(t, nil)
	h.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("done"), nil)},
	})

	props := omap.NewMap[string, *typebox.Schema]()
	props.Set("text", typebox.String())
	tool := &ai.Tool{
		Name:        "echo",
		Description: "Echo back text",
		Parameters:  typebox.Object(props),
	}
	sys := "sys"
	resp, err := models.Complete(context.Background(), h.GetModel(), &ai.Context{
		SystemPrompt: &sys,
		Messages: ai.MessageList{
			&ai.UserMessage{
				Role: "user",
				Content: ai.UserMessageContent{Blocks: ai.ContentList{
					&ai.TextContent{Type: "text", Text: "hello"},
					&ai.ImageContent{Type: "image", MimeType: "image/png", Data: "abcd"},
				}},
				Timestamp: 1,
			},
			providers.FauxAssistantMessage(providers.FauxAssistantText("prior"), nil),
			&ai.ToolResultMessage{
				Role:       "toolResult",
				ToolCallId: "tool-1",
				ToolName:   "echo",
				Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: "tool out"}},
				IsError:    false,
				Timestamp:  2,
			},
		},
		Tools: []*ai.Tool{tool},
	}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	promptText := strings.Join([]string{
		"system:sys\ntool+:" + jsonx.MustStringify(tool),
		"user:hello\n[image:image/png:4]",
		"assistant:prior",
		"toolResult:echo\ntool out",
	}, "\n\n")
	expectedPromptTokens := (js.Len(promptText) + 3) / 4
	expectedOutputTokens := (js.Len("done") + 3) / 4
	if resp.Usage.Input != expectedPromptTokens || resp.Usage.Output != expectedOutputTokens ||
		resp.Usage.CacheRead != 0 || resp.Usage.CacheWrite != 0 ||
		resp.Usage.TotalTokens != expectedPromptTokens+expectedOutputTokens {
		t.Fatalf("usage mismatch: got %+v want input=%d output=%d", resp.Usage, expectedPromptTokens, expectedOutputTokens)
	}
}

func TestFauxProvider_PromptCaching(t *testing.T) {
	h, models := tfSetupFauxModels(t, nil)
	h.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("first"), nil)},
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("second"), nil)},
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("third"), nil)},
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("a"), nil)},
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("b"), nil)},
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("c"), nil)},
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("n1"), nil)},
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("n2"), nil)},
	})

	short := ai.CacheRetentionShort
	none := ai.CacheRetentionNone
	s1 := "session-1"
	s2 := "session-2"

	msgs := ai.MessageList{tfUserMsg("hello", 1)}
	first, _ := models.Complete(context.Background(), h.GetModel(), &ai.Context{Messages: msgs}, &ai.ModelsApiStreamOptions{
		ProviderStreamOptions: &ai.StreamOptions{SessionId: &s1, CacheRetention: &short},
	})
	if first.Usage.CacheWrite <= 0 {
		t.Fatalf("expected cacheWrite > 0, got %+v", first.Usage)
	}

	msgs2 := append(slices.Clone(msgs), first, tfUserMsg("follow up", 2))
	second, _ := models.Complete(context.Background(), h.GetModel(), &ai.Context{Messages: msgs2}, &ai.ModelsApiStreamOptions{
		ProviderStreamOptions: &ai.StreamOptions{SessionId: &s2, CacheRetention: &short},
	})
	if second.Usage.CacheRead != 0 || second.Usage.CacheWrite <= 0 {
		t.Fatalf("expected isolated session-2 cacheWrite, got %+v", second.Usage)
	}

	third, _ := models.Complete(context.Background(), h.GetModel(), &ai.Context{Messages: msgs2}, nil)
	if third.Usage.CacheRead != 0 || third.Usage.CacheWrite != 0 {
		t.Fatalf("expected no cache without sessionId, got %+v", third.Usage)
	}

	// Prefix diff check: "user:hello world" (16) -> 4 tokens, then + "\n\nuser:next" (+11 = 27) -> 4 read, 3 write, then edited "hello wide" -> common "user:hello w" (12) -> 3 read, 4 write.
	s3 := "session-prefix"
	optsPrefix := &ai.ModelsApiStreamOptions{
		ProviderStreamOptions: &ai.StreamOptions{SessionId: &s3, CacheRetention: &short},
	}
	p1, _ := models.Complete(context.Background(), h.GetModel(), &ai.Context{Messages: ai.MessageList{tfUserMsg("hello world", 1)}}, optsPrefix)
	if p1.Usage.Input != 0 || p1.Usage.CacheRead != 0 || p1.Usage.CacheWrite != 4 {
		t.Fatalf("prefix p1 mismatch: %+v", p1.Usage)
	}
	p2, _ := models.Complete(context.Background(), h.GetModel(), &ai.Context{Messages: ai.MessageList{tfUserMsg("hello world", 1), tfUserMsg("next", 1)}}, optsPrefix)
	if p2.Usage.Input != 0 || p2.Usage.CacheRead != 4 || p2.Usage.CacheWrite != 3 {
		t.Fatalf("prefix p2 mismatch: %+v", p2.Usage)
	}
	p3, _ := models.Complete(context.Background(), h.GetModel(), &ai.Context{Messages: ai.MessageList{tfUserMsg("hello wide", 1), tfUserMsg("next", 1)}}, optsPrefix)
	if p3.Usage.Input != 0 || p3.Usage.CacheRead != 3 || p3.Usage.CacheWrite != 4 {
		t.Fatalf("prefix p3 mismatch: %+v", p3.Usage)
	}

	optsNone := &ai.ModelsApiStreamOptions{
		ProviderStreamOptions: &ai.StreamOptions{SessionId: &s1, CacheRetention: &none},
	}
	_, _ = models.Complete(context.Background(), h.GetModel(), &ai.Context{Messages: ai.MessageList{tfUserMsg("hello", 1)}}, optsNone)
	n2, _ := models.Complete(context.Background(), h.GetModel(), &ai.Context{Messages: ai.MessageList{tfUserMsg("hello", 1), tfUserMsg("follow up", 2)}}, optsNone)
	if n2.Usage.CacheRead != 0 || n2.Usage.CacheWrite != 0 {
		t.Fatalf("expected cacheRetention=none to skip cache, got %+v", n2.Usage)
	}
}

func TestFauxProvider_StreamsAndEventOrder(t *testing.T) {
	h, models := tfSetupFauxModels(t, &providers.RegisterFauxProviderOptions{
		TokenSize: &providers.FauxTokenSizeOptions{Min: tfPtr(1), Max: tfPtr(1)},
	})
	toolUse := ai.StopReasonToolUse
	h.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
				providers.FauxThinking("go"),
				providers.FauxText("ok"),
				providers.FauxToolCall("echo", jsonx.NewObject(), &providers.FauxToolCallOptions{Id: tfPtr("tool-1")}),
			}, &providers.FauxAssistantMessageOptions{StopReason: &toolUse}),
		},
		providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
				providers.FauxToolCall("echo", jsonx.ObjectOf("text", "hi", "count", float64(12)), &providers.FauxToolCallOptions{Id: tfPtr("tool-1")}),
			}, &providers.FauxAssistantMessageOptions{StopReason: &toolUse}),
		},
	})

	events := tfCollectEvents(t, models.Stream(context.Background(), h.GetModel(), &ai.Context{
		Messages: ai.MessageList{tfUserMsg("hi", js.DateNow())},
	}, nil))
	wantOrder := []string{
		"start",
		"thinking_start", "thinking_delta", "thinking_end",
		"text_start", "text_delta", "text_end",
		"toolcall_start", "toolcall_delta", "toolcall_end",
		"done",
	}
	if !reflect.DeepEqual(tfEventTypes(events), wantOrder) {
		t.Fatalf("order mismatch:\ngot:  %v\nwant: %v", tfEventTypes(events), wantOrder)
	}
	startEv := events[0].(*ai.AssistantMessageEventStart)
	if startEv.Partial.StopReason != ai.StopReasonPending {
		t.Fatalf("expected start partial stopReason=pending, got %q", startEv.Partial.StopReason)
	}

	events2 := tfCollectEvents(t, models.Stream(context.Background(), h.GetModel(), &ai.Context{
		Messages: ai.MessageList{tfUserMsg("hi", js.DateNow())},
	}, nil))
	var deltas strings.Builder
	deltaCount := 0
	for _, ev := range events2 {
		if td, ok := ev.(*ai.AssistantMessageEventToolcallDelta); ok {
			deltas.WriteString(td.Delta)
			deltaCount++
		}
	}
	if deltaCount <= 1 {
		t.Fatalf("expected >1 toolcall deltas, got %d", deltaCount)
	}
	parsed, err := jsonx.ParseString(deltas.String())
	if err != nil {
		t.Fatalf("ParseString(%q): %v", deltas.String(), err)
	}
	if !jsonx.DeepEqual(parsed, jsonx.ObjectOf("text", "hi", "count", float64(12))) {
		t.Fatalf("parsed tool deltas mismatch: %#v", parsed)
	}
}

func TestFauxProvider_ExplicitErrorAndAborted(t *testing.T) {
	h, models := tfSetupFauxModels(t, &providers.RegisterFauxProviderOptions{
		TokenSize: &providers.FauxTokenSizeOptions{Min: tfPtr(2), Max: tfPtr(2)},
	})
	errStop := ai.StopReasonError
	abortedStop := ai.StopReasonAborted
	h.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("partial"), &providers.FauxAssistantMessageOptions{
			StopReason:   &errStop,
			ErrorMessage: tfPtr("upstream failed"),
		})},
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("partial"), &providers.FauxAssistantMessageOptions{
			StopReason:   &abortedStop,
			ErrorMessage: tfPtr("Request was aborted"),
		})},
	})

	want := []string{"start", "text_start", "text_delta", "text_end", "error"}
	ev1 := tfCollectEvents(t, models.Stream(context.Background(), h.GetModel(), &ai.Context{Messages: ai.MessageList{tfUserMsg("hi", 1)}}, nil))
	if !reflect.DeepEqual(tfEventTypes(ev1), want) {
		t.Fatalf("error sequence mismatch: %v", tfEventTypes(ev1))
	}
	if term := ev1[len(ev1)-1].(*ai.AssistantMessageEventError); term.Reason != ai.StopReasonError || term.Error.ErrorMessage == nil || *term.Error.ErrorMessage != "upstream failed" {
		t.Fatalf("unexpected error terminal: %+v", term)
	}

	ev2 := tfCollectEvents(t, models.Stream(context.Background(), h.GetModel(), &ai.Context{Messages: ai.MessageList{tfUserMsg("hi", 1)}}, nil))
	if !reflect.DeepEqual(tfEventTypes(ev2), want) {
		t.Fatalf("aborted sequence mismatch: %v", tfEventTypes(ev2))
	}
	if term := ev2[len(ev2)-1].(*ai.AssistantMessageEventError); term.Reason != ai.StopReasonAborted || term.Error.ErrorMessage == nil || *term.Error.ErrorMessage != "Request was aborted" {
		t.Fatalf("unexpected aborted terminal: %+v", term)
	}
}

func TestFauxProvider_AbortBeforeAndMidStream(t *testing.T) {
	tps50 := 50.0
	h, models := tfSetupFauxModels(t, &providers.RegisterFauxProviderOptions{
		TokensPerSecond: &tps50,
		TokenSize:       &providers.FauxTokenSizeOptions{Min: tfPtr(3), Max: tfPtr(3)},
	})
	h.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("abcdefghijklmnopqrstuvwxyz"), nil)},
	})
	ctx0, cancel0 := context.WithCancel(context.Background())
	cancel0()
	ev0 := tfCollectEvents(t, models.Stream(ctx0, h.GetModel(), &ai.Context{Messages: ai.MessageList{tfUserMsg("hi", 1)}}, nil))
	if len(ev0) != 1 || ev0[0].(*ai.AssistantMessageEventError).Reason != ai.StopReasonAborted {
		t.Fatalf("expected immediate abort event, got %#v", ev0)
	}

	tps100 := 100.0
	h2, models2 := tfSetupFauxModels(t, &providers.RegisterFauxProviderOptions{
		TokensPerSecond: &tps100,
		TokenSize:       &providers.FauxTokenSizeOptions{Min: tfPtr(3), Max: tfPtr(3)},
	})
	toolUse := ai.StopReasonToolUse
	h2.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("abcdefghijklmnopqrstuvwxyz"), nil)},
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlock{Block: providers.FauxThinking("abcdefghijklmnopqrstuvwxyz")}, nil)},
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlock{Block: providers.FauxToolCall("echo", jsonx.ObjectOf("text", "abcdefghijklmnopqrstuvwxyz", "count", float64(123456789)), &providers.FauxToolCallOptions{Id: tfPtr("tool-1")})}, &providers.FauxAssistantMessageOptions{StopReason: &toolUse})},
	})

	runAbortMid := func(deltaType, endType string) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var types []string
		deltaCount := 0
		s := models2.Stream(ctx, h2.GetModel(), &ai.Context{Messages: ai.MessageList{tfUserMsg("hi", 1)}}, nil)
		for ev := range s.All() {
			tp := tfEventTypes([]ai.AssistantMessageEvent{ev})[0]
			types = append(types, tp)
			if tp == deltaType {
				deltaCount++
				cancel()
			}
		}
		if deltaCount != 1 {
			t.Fatalf("%s: expected 1 delta before abort, got %d (%v)", deltaType, deltaCount, types)
		}
		if !slices.Contains(types, "error") || slices.Contains(types, endType) {
			t.Fatalf("%s: unexpected event types after abort: %v", deltaType, types)
		}
	}

	runAbortMid("text_delta", "text_end")
	runAbortMid("thinking_delta", "thinking_end")
	runAbortMid("toolcall_delta", "toolcall_end")
}

func TestFauxProvider_DeferredLifecycle(t *testing.T) {
	h, models := tfSetupFauxModels(t, &providers.RegisterFauxProviderOptions{
		Deferred: &providers.FauxDeferredOptions{
			PendingFetches: tfPtr(1),
			PollAfterMs:    tfPtr(int64(250)),
		},
	})
	h.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("ready later"), nil)},
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("will cancel"), nil)},
	})

	sub1, err := models.Complete(context.Background(), h.GetModel(), &ai.Context{
		Messages: ai.MessageList{tfUserMsg("hi", 1)},
	}, &ai.ModelsApiStreamOptions{
		ProviderStreamOptions: &ai.SimpleStreamOptions{
			Deferred: ai.SimpleStreamDeferredValue{V: ai.SimpleStreamDeferredBool(true)},
		},
	})
	if err != nil {
		t.Fatalf("deferred Complete: %v", err)
	}
	if sub1.StopReason != ai.StopReasonDeferred || sub1.Deferred == nil || sub1.Deferred.PollAfterMs == nil || *sub1.Deferred.PollAfterMs != 250 {
		t.Fatalf("unexpected deferred response: %+v", sub1)
	}

	pending, err := models.FetchDeferred(context.Background(), h.GetModel(), sub1.Deferred, nil)
	if err != nil {
		t.Fatalf("FetchDeferred pending: %v", err)
	}
	if pending.StopReason != ai.StopReasonDeferred {
		t.Fatalf("expected still deferred on first fetch, got %q", pending.StopReason)
	}

	ready, err := models.FetchDeferred(context.Background(), h.GetModel(), sub1.Deferred, nil)
	if err != nil {
		t.Fatalf("FetchDeferred ready: %v", err)
	}
	if ready.StopReason != ai.StopReasonStop || ready.Content[0].(*ai.TextContent).Text != "ready later" {
		t.Fatalf("unexpected ready response: %+v", ready)
	}
	if h.State.DeferredFetchCount != 2 {
		t.Fatalf("expected DeferredFetchCount=2, got %d", h.State.DeferredFetchCount)
	}

	sub2, _ := models.Complete(context.Background(), h.GetModel(), &ai.Context{
		Messages: ai.MessageList{tfUserMsg("hi2", 2)},
	}, &ai.ModelsApiStreamOptions{
		ProviderStreamOptions: &ai.SimpleStreamOptions{
			Deferred: ai.SimpleStreamDeferredValue{V: ai.SimpleStreamDeferredBool(true)},
		},
	})
	if err := models.CancelDeferred(context.Background(), h.GetModel(), sub2.Deferred, nil); err != nil {
		t.Fatalf("CancelDeferred: %v", err)
	}
	if len(h.State.CancelledDeferred) != 1 || h.State.CancelledDeferred[0].Id != sub2.Deferred.Id {
		t.Fatalf("expected cancelled handle recorded, got %+v", h.State.CancelledDeferred)
	}
	afterCancel, _ := models.FetchDeferred(context.Background(), h.GetModel(), sub2.Deferred, nil)
	if afterCancel.StopReason != ai.StopReasonError || !strings.Contains(*afterCancel.ErrorMessage, "cancelled") {
		t.Fatalf("expected cancelled error, got %+v", afterCancel)
	}
}
