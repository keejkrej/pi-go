// Ported from packages/ai/test/faux-provider.test.ts (pi v1.5.2).

package compat_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/compat"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

func tfpPtr[T any](v T) *T { return &v }

func tfpRegister(t *testing.T, opts *providers.RegisterFauxProviderOptions) *providers.FauxProviderRegistration {
	t.Helper()
	reg := compat.RegisterFauxProvider(opts)
	t.Cleanup(reg.Unregister)
	return reg
}

func tfpUserMsg(text string, ts int64) *ai.UserMessage {
	return &ai.UserMessage{
		Role:      "user",
		Content:   ai.UserMessageContent{Text: &text},
		Timestamp: ts,
	}
}

func tfpCollectEvents(t *testing.T, s *ai.AssistantMessageEventStream) []ai.AssistantMessageEvent {
	t.Helper()
	var events []ai.AssistantMessageEvent
	for ev := range s.All() {
		events = append(events, ev)
	}
	return events
}

func tfpEventType(ev ai.AssistantMessageEvent) string {
	switch e := ev.(type) {
	case *ai.AssistantMessageEventStart:
		return e.Type
	case *ai.AssistantMessageEventTextStart:
		return e.Type
	case *ai.AssistantMessageEventTextDelta:
		return e.Type
	case *ai.AssistantMessageEventTextEnd:
		return e.Type
	case *ai.AssistantMessageEventThinkingStart:
		return e.Type
	case *ai.AssistantMessageEventThinkingDelta:
		return e.Type
	case *ai.AssistantMessageEventThinkingEnd:
		return e.Type
	case *ai.AssistantMessageEventToolcallStart:
		return e.Type
	case *ai.AssistantMessageEventToolcallDelta:
		return e.Type
	case *ai.AssistantMessageEventToolcallEnd:
		return e.Type
	case *ai.AssistantMessageEventDone:
		return e.Type
	case *ai.AssistantMessageEventError:
		return e.Type
	default:
		return ""
	}
}

func tfpEventTypes(events []ai.AssistantMessageEvent) []string {
	out := make([]string, len(events))
	for i, ev := range events {
		out[i] = tfpEventType(ev)
	}
	return out
}

func tfpMustComplete(t *testing.T, ctx context.Context, model *ai.Model, c *ai.Context, opts *ai.StreamOptions) *ai.AssistantMessage {
	t.Helper()
	resp, err := compat.Complete(ctx, model, c, opts)
	if err != nil {
		t.Fatalf("compat.Complete failed: %v", err)
	}
	return resp
}

func TestFauxProviderCompat_UsageAndDefaults(t *testing.T) {
	reg := tfpRegister(t, nil)
	reg.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("hello world"), nil)},
	})

	sys := "Be concise."
	resp := tfpMustComplete(t, context.Background(), reg.GetModel(), &ai.Context{
		SystemPrompt: &sys,
		Messages:     ai.MessageList{tfpUserMsg("hi there", js.DateNow())},
	}, nil)

	if len(resp.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(resp.Content))
	}
	tc, ok := resp.Content[0].(*ai.TextContent)
	if !ok || tc.Text != "hello world" {
		t.Fatalf("unexpected content: %+v", resp.Content[0])
	}
	if resp.Usage.Input <= 0 || resp.Usage.Output <= 0 || resp.Usage.TotalTokens != resp.Usage.Input+resp.Usage.Output {
		t.Fatalf("unexpected usage: %+v", resp.Usage)
	}
	if reg.State.CallCount != 1 {
		t.Fatalf("callCount = %d, want 1", reg.State.CallCount)
	}
}

func TestFauxProviderCompat_HelperBlocks(t *testing.T) {
	reg := tfpRegister(t, nil)
	args := jsonx.NewObject()
	args.Set("text", "hi")
	toolStop := ai.StopReasonToolUse
	reg.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(
				providers.FauxAssistantBlocks{
					providers.FauxThinking("think"),
					providers.FauxToolCall("echo", args, nil),
					providers.FauxText("done"),
				},
				&providers.FauxAssistantMessageOptions{StopReason: &toolStop},
			),
		},
	})

	resp := tfpMustComplete(t, context.Background(), reg.GetModel(), &ai.Context{
		Messages: ai.MessageList{tfpUserMsg("hi", js.DateNow())},
	}, nil)

	if resp.StopReason != ai.StopReasonToolUse || len(resp.Content) != 3 {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if th, ok := resp.Content[0].(*ai.ThinkingContent); !ok || th.Thinking != "think" {
		t.Fatalf("block 0 = %+v", resp.Content[0])
	}
	if call, ok := resp.Content[1].(*ai.ToolCall); !ok || call.Name != "echo" || call.Id == "" {
		t.Fatalf("block 1 = %+v", resp.Content[1])
	}
	if tx, ok := resp.Content[2].(*ai.TextContent); !ok || tx.Text != "done" {
		t.Fatalf("block 2 = %+v", resp.Content[2])
	}
}

func TestFauxProviderCompat_MultipleModelsAndFactories(t *testing.T) {
	reg := tfpRegister(t, &providers.RegisterFauxProviderOptions{
		Models: []providers.FauxModelDefinition{
			{Id: "faux-fast", Name: tfpPtr("Faux Fast"), Reasoning: tfpPtr(false)},
			{Id: "faux-thinker", Name: tfpPtr("Faux Thinker"), Reasoning: tfpPtr(true)},
		},
	})
	factory := providers.FauxResponseFactory(func(_ context.Context, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, m *ai.Model) (*ai.AssistantMessage, error) {
		r := "false"
		if m.Reasoning {
			r = "true"
		}
		return providers.FauxAssistantMessage(providers.FauxAssistantText(m.Id+":"+r), nil), nil
	})
	reg.SetResponses([]providers.FauxResponseStep{factory, factory})

	if len(reg.Models) != 2 || reg.Models[0].Id != "faux-fast" || reg.Models[1].Id != "faux-thinker" {
		t.Fatalf("unexpected models: %+v", reg.Models)
	}
	if reg.GetModel() != reg.Models[0] {
		t.Fatal("GetModel() != Models[0]")
	}
	if reg.GetModelId("faux-fast").Reasoning != false || reg.GetModelId("faux-thinker").Reasoning != true {
		t.Fatal("unexpected reasoning metadata")
	}

	fast := tfpMustComplete(t, context.Background(), reg.GetModelId("faux-fast"), &ai.Context{
		Messages: ai.MessageList{tfpUserMsg("hi", js.DateNow())},
	}, nil)
	thinker := tfpMustComplete(t, context.Background(), reg.GetModelId("faux-thinker"), &ai.Context{
		Messages: ai.MessageList{tfpUserMsg("hi", js.DateNow())},
	}, nil)

	if fast.Content[0].(*ai.TextContent).Text != "faux-fast:false" {
		t.Fatalf("fast content = %+v", fast.Content)
	}
	if thinker.Content[0].(*ai.TextContent).Text != "faux-thinker:true" {
		t.Fatalf("thinker content = %+v", thinker.Content)
	}
}

func TestFauxProviderCompat_RewritesMetadata(t *testing.T) {
	reg := tfpRegister(t, &providers.RegisterFauxProviderOptions{
		Api:      tfpPtr("faux:test"),
		Provider: tfpPtr("faux-provider"),
		Models:   []providers.FauxModelDefinition{{Id: "faux-model"}},
	})
	reg.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("hello"), nil)},
	})

	resp := tfpMustComplete(t, context.Background(), reg.GetModel(), &ai.Context{
		Messages: ai.MessageList{tfpUserMsg("hi", js.DateNow())},
	}, nil)
	if resp.Api != "faux:test" || resp.Provider != "faux-provider" || resp.Model != "faux-model" {
		t.Fatalf("unexpected metadata: api=%s provider=%s model=%s", resp.Api, resp.Provider, resp.Model)
	}
}

func TestFauxProviderCompat_QueueOrderAndExhaustion(t *testing.T) {
	reg := tfpRegister(t, nil)
	reg.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("first"), nil)},
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("second"), nil)},
	})
	reqCtx := &ai.Context{Messages: ai.MessageList{tfpUserMsg("hi", js.DateNow())}}

	first := tfpMustComplete(t, context.Background(), reg.GetModel(), reqCtx, nil)
	second := tfpMustComplete(t, context.Background(), reg.GetModel(), reqCtx, nil)
	exhausted := tfpMustComplete(t, context.Background(), reg.GetModel(), reqCtx, nil)

	if first.Content[0].(*ai.TextContent).Text != "first" || second.Content[0].(*ai.TextContent).Text != "second" {
		t.Fatalf("unexpected first/second responses")
	}
	if exhausted.StopReason != ai.StopReasonError || exhausted.ErrorMessage == nil || *exhausted.ErrorMessage != "No more faux responses queued" {
		t.Fatalf("unexpected exhausted response: %+v", exhausted)
	}
	if reg.GetPendingResponseCount() != 0 || reg.State.CallCount != 3 {
		t.Fatalf("pending=%d callCount=%d", reg.GetPendingResponseCount(), reg.State.CallCount)
	}
}

func TestFauxProviderCompat_SetAndAppendResponses(t *testing.T) {
	reg := tfpRegister(t, nil)
	reg.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("first"), nil)},
	})
	reqCtx := &ai.Context{Messages: ai.MessageList{tfpUserMsg("hi", js.DateNow())}}

	if got := tfpMustComplete(t, context.Background(), reg.GetModel(), reqCtx, nil).Content[0].(*ai.TextContent).Text; got != "first" {
		t.Fatalf("got %q, want first", got)
	}
	if reg.GetPendingResponseCount() != 0 {
		t.Fatalf("pending = %d, want 0", reg.GetPendingResponseCount())
	}

	reg.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("second"), nil)},
	})
	if reg.GetPendingResponseCount() != 1 {
		t.Fatalf("pending = %d, want 1", reg.GetPendingResponseCount())
	}
	if got := tfpMustComplete(t, context.Background(), reg.GetModel(), reqCtx, nil).Content[0].(*ai.TextContent).Text; got != "second" {
		t.Fatalf("got %q, want second", got)
	}

	reg.AppendResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("third"), nil)},
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("fourth"), nil)},
	})
	if reg.GetPendingResponseCount() != 2 {
		t.Fatalf("pending = %d, want 2", reg.GetPendingResponseCount())
	}
	if got := tfpMustComplete(t, context.Background(), reg.GetModel(), reqCtx, nil).Content[0].(*ai.TextContent).Text; got != "third" {
		t.Fatalf("got %q, want third", got)
	}
	if got := tfpMustComplete(t, context.Background(), reg.GetModel(), reqCtx, nil).Content[0].(*ai.TextContent).Text; got != "fourth" {
		t.Fatalf("got %q, want fourth", got)
	}
	if reg.GetPendingResponseCount() != 0 {
		t.Fatalf("pending = %d, want 0", reg.GetPendingResponseCount())
	}
}

func TestFauxProviderCompat_FactoryStateAndError(t *testing.T) {
	reg := tfpRegister(t, nil)
	reg.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseFactory(func(_ context.Context, c *ai.TranscriptContext, _ *ai.SimpleStreamOptions, state *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
			txt := string(rune('0'+len(c.Messages))) + ":" + string(rune('0'+state.CallCount))
			return providers.FauxAssistantMessage(providers.FauxAssistantText(txt), nil), nil
		}),
	})
	resp := tfpMustComplete(t, context.Background(), reg.GetModel(), &ai.Context{
		Messages: ai.MessageList{tfpUserMsg("hi", js.DateNow())},
	}, nil)
	if resp.Content[0].(*ai.TextContent).Text != "1:1" {
		t.Fatalf("got %q, want 1:1", resp.Content[0].(*ai.TextContent).Text)
	}

	reg.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseFactory(func(_ context.Context, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
			return nil, errors.New("boom")
		}),
	})
	events := tfpCollectEvents(t, compat.Stream(context.Background(), reg.GetModel(), &ai.Context{
		Messages: ai.MessageList{tfpUserMsg("hi", js.DateNow())},
	}, nil))
	if len(events) != 1 {
		t.Fatalf("expected 1 error event, got %d", len(events))
	}
	errEv, ok := events[0].(*ai.AssistantMessageEventError)
	if !ok || errEv.Error.StopReason != ai.StopReasonError || errEv.Error.ErrorMessage == nil || *errEv.Error.ErrorMessage != "boom" {
		t.Fatalf("unexpected error event: %+v", events[0])
	}
}

func TestFauxProviderCompat_RejectsPendingStopReason(t *testing.T) {
	reg := tfpRegister(t, nil)
	pending := ai.StopReasonPending
	reg.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(
				providers.FauxAssistantText("partial"),
				&providers.FauxAssistantMessageOptions{StopReason: &pending},
			),
		},
	})
	events := tfpCollectEvents(t, compat.Stream(context.Background(), reg.GetModel(), &ai.Context{
		Messages: ai.MessageList{tfpUserMsg("hi", js.DateNow())},
	}, nil))
	if slices.Contains(tfpEventTypes(events), "done") {
		t.Fatalf("unexpected done event in %v", tfpEventTypes(events))
	}
	terminal, ok := events[len(events)-1].(*ai.AssistantMessageEventError)
	if !ok || terminal.Error.StopReason != ai.StopReasonError || terminal.Error.ErrorMessage == nil || *terminal.Error.ErrorMessage != "Faux response ended without a stop reason" {
		t.Fatalf("unexpected terminal event: %+v", events[len(events)-1])
	}
}

func TestFauxProviderCompat_PromptAndOutputTokenEstimate(t *testing.T) {
	reg := tfpRegister(t, nil)
	reg.SetResponses([]providers.FauxResponseStep{
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
	resp := tfpMustComplete(t, context.Background(), reg.GetModel(), &ai.Context{
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
		t.Fatalf("usage = %+v, want input=%d output=%d", resp.Usage, expectedPromptTokens, expectedOutputTokens)
	}
}

func TestFauxProviderCompat_PromptCaching(t *testing.T) {
	reg := tfpRegister(t, nil)
	ctx := context.Background()
	model := reg.GetModel()
	short := ai.CacheRetentionShort
	none := ai.CacheRetentionNone

	// Session isolation and missing sessionId
	reg.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("first"), nil)},
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("second"), nil)},
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("third"), nil)},
	})
	reqCtx := &ai.Context{Messages: ai.MessageList{tfpUserMsg("hello", js.DateNow())}}
	first := tfpMustComplete(t, ctx, model, reqCtx, &ai.StreamOptions{SessionId: tfpPtr("session-1"), CacheRetention: &short})
	if first.Usage.CacheWrite <= 0 {
		t.Fatalf("expected cacheWrite > 0, got %d", first.Usage.CacheWrite)
	}
	reqCtx.Messages = append(reqCtx.Messages, first, tfpUserMsg("follow up", js.DateNow()+1))
	second := tfpMustComplete(t, ctx, model, reqCtx, &ai.StreamOptions{SessionId: tfpPtr("session-2"), CacheRetention: &short})
	if second.Usage.CacheRead != 0 || second.Usage.CacheWrite <= 0 {
		t.Fatalf("unexpected second session usage: %+v", second.Usage)
	}
	third := tfpMustComplete(t, ctx, model, reqCtx, nil)
	if third.Usage.CacheRead != 0 || third.Usage.CacheWrite != 0 {
		t.Fatalf("expected zero cache without sessionId, got %+v", third.Usage)
	}

	// Same session cache hits + common prefix
	reg2 := tfpRegister(t, nil)
	reg2.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("a"), nil)},
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("b"), nil)},
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("c"), nil)},
	})
	opts := &ai.StreamOptions{SessionId: tfpPtr("session-1"), CacheRetention: &short}
	r1 := tfpMustComplete(t, ctx, reg2.GetModel(), &ai.Context{Messages: ai.MessageList{tfpUserMsg("hello world", 1)}}, opts)
	if r1.Usage.Input != 0 || r1.Usage.CacheRead != 0 || r1.Usage.CacheWrite != 4 {
		t.Fatalf("r1 usage = %+v", r1.Usage)
	}
	r2 := tfpMustComplete(t, ctx, reg2.GetModel(), &ai.Context{Messages: ai.MessageList{tfpUserMsg("hello world", 1), tfpUserMsg("next", 1)}}, opts)
	if r2.Usage.Input != 0 || r2.Usage.CacheRead != 4 || r2.Usage.CacheWrite != 3 {
		t.Fatalf("r2 usage = %+v", r2.Usage)
	}
	r3 := tfpMustComplete(t, ctx, reg2.GetModel(), &ai.Context{Messages: ai.MessageList{tfpUserMsg("hello wide", 1), tfpUserMsg("next", 1)}}, opts)
	if r3.Usage.Input != 0 || r3.Usage.CacheRead != 3 || r3.Usage.CacheWrite != 4 {
		t.Fatalf("r3 usage = %+v", r3.Usage)
	}

	// cacheRetention: none disables caching
	reg3 := tfpRegister(t, nil)
	reg3.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("first"), nil)},
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("second"), nil)},
	})
	noneCtx := &ai.Context{Messages: ai.MessageList{tfpUserMsg("hello", js.DateNow())}}
	noneOpts := &ai.StreamOptions{SessionId: tfpPtr("session-1"), CacheRetention: &none}
	_ = tfpMustComplete(t, ctx, reg3.GetModel(), noneCtx, noneOpts)
	noneCtx.Messages = append(noneCtx.Messages, providers.FauxAssistantMessage(providers.FauxAssistantText("first"), nil), tfpUserMsg("follow up", js.DateNow()+1))
	n2 := tfpMustComplete(t, ctx, reg3.GetModel(), noneCtx, noneOpts)
	if n2.Usage.CacheRead != 0 || n2.Usage.CacheWrite != 0 {
		t.Fatalf("expected zero cache when none, got %+v", n2.Usage)
	}
}

func TestFauxProviderCompat_StreamingEventsAndOrder(t *testing.T) {
	reg := tfpRegister(t, nil)
	args := jsonx.NewObject()
	args.Set("text", "hi")
	args.Set("count", 12)
	toolStop := ai.StopReasonToolUse
	reg.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(
				providers.FauxAssistantBlocks{
					providers.FauxThinking("thinking text"),
					providers.FauxText("answer text"),
					providers.FauxToolCall("echo", args, &providers.FauxToolCallOptions{Id: tfpPtr("tool-1")}),
				},
				&providers.FauxAssistantMessageOptions{StopReason: &toolStop},
			),
		},
	})
	events := tfpCollectEvents(t, compat.Stream(context.Background(), reg.GetModel(), &ai.Context{
		Messages: ai.MessageList{tfpUserMsg("hi", js.DateNow())},
	}, nil))
	var toolCallDeltas []string
	for _, ev := range events {
		if d, ok := ev.(*ai.AssistantMessageEventToolcallDelta); ok {
			toolCallDeltas = append(toolCallDeltas, d.Delta)
		}
	}
	types := tfpEventTypes(events)
	for _, want := range []string{"thinking_start", "thinking_delta", "text_start", "text_delta", "toolcall_start", "toolcall_delta", "toolcall_end"} {
		if !slices.Contains(types, want) {
			t.Fatalf("missing event type %s in %v", want, types)
		}
	}
	if len(toolCallDeltas) <= 1 {
		t.Fatalf("expected >1 toolcall_delta, got %v", toolCallDeltas)
	}
	parsed, err := jsonx.Parse([]byte(strings.Join(toolCallDeltas, "")))
	if err != nil {
		t.Fatalf("parse toolcall deltas: %v", err)
	}
	obj, ok := parsed.(*jsonx.Object)
	if !ok {
		t.Fatalf("expected *jsonx.Object, got %T", parsed)
	}
	if v, _ := obj.Get("text"); v != "hi" {
		t.Fatalf("unexpected text arg: %v", v)
	}

	// Fixed chunk size = 1 exact event order
	min1, max1 := 1, 1
	regFixed := tfpRegister(t, &providers.RegisterFauxProviderOptions{
		TokenSize: &providers.FauxTokenSizeOptions{Min: &min1, Max: &max1},
	})
	regFixed.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(
				providers.FauxAssistantBlocks{
					providers.FauxThinking("go"),
					providers.FauxText("ok"),
					providers.FauxToolCall("echo", jsonx.NewObject(), &providers.FauxToolCallOptions{Id: tfpPtr("tool-1")}),
				},
				&providers.FauxAssistantMessageOptions{StopReason: &toolStop},
			),
		},
	})
	fixedEvents := tfpCollectEvents(t, compat.Stream(context.Background(), regFixed.GetModel(), &ai.Context{
		Messages: ai.MessageList{tfpUserMsg("hi", js.DateNow())},
	}, nil))
	if st, ok := fixedEvents[0].(*ai.AssistantMessageEventStart); !ok || st.Partial.StopReason != ai.StopReasonPending {
		t.Fatalf("unexpected start event: %+v", fixedEvents[0])
	}
	wantOrder := []string{
		"start",
		"thinking_start",
		"thinking_delta",
		"thinking_end",
		"text_start",
		"text_delta",
		"text_end",
		"toolcall_start",
		"toolcall_delta",
		"toolcall_end",
		"done",
	}
	if !slices.Equal(tfpEventTypes(fixedEvents), wantOrder) {
		t.Fatalf("event order = %v, want %v", tfpEventTypes(fixedEvents), wantOrder)
	}

	// Multiple tool calls
	regMulti := tfpRegister(t, nil)
	a1, a2 := jsonx.NewObject(), jsonx.NewObject()
	a1.Set("text", "one")
	a2.Set("text", "two")
	regMulti.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(
				providers.FauxAssistantBlocks{
					providers.FauxToolCall("echo", a1, &providers.FauxToolCallOptions{Id: tfpPtr("tool-1")}),
					providers.FauxToolCall("echo", a2, &providers.FauxToolCallOptions{Id: tfpPtr("tool-2")}),
				},
				&providers.FauxAssistantMessageOptions{StopReason: &toolStop},
			),
		},
	})
	multiTypes := tfpEventTypes(tfpCollectEvents(t, compat.Stream(context.Background(), regMulti.GetModel(), &ai.Context{
		Messages: ai.MessageList{tfpUserMsg("hi", js.DateNow())},
	}, nil)))
	starts, ends := 0, 0
	for _, typ := range multiTypes {
		if typ == "toolcall_start" {
			starts++
		} else if typ == "toolcall_end" {
			ends++
		}
	}
	if starts != 2 || ends != 2 {
		t.Fatalf("starts=%d ends=%d in %v", starts, ends, multiTypes)
	}
}

func TestFauxProviderCompat_ExplicitErrorAbortedAndMidStreamAbort(t *testing.T) {
	s2 := 2
	reg := tfpRegister(t, &providers.RegisterFauxProviderOptions{
		TokenSize: &providers.FauxTokenSizeOptions{Min: &s2, Max: &s2},
	})
	errMsg1 := providers.FauxAssistantMessage(providers.FauxAssistantText("partial"), nil)
	errMsg1.StopReason = ai.StopReasonError
	errMsg1.ErrorMessage = tfpPtr("upstream failed")
	reg.SetResponses([]providers.FauxResponseStep{providers.FauxResponseMessage{Message: errMsg1}})

	e1 := tfpCollectEvents(t, compat.Stream(context.Background(), reg.GetModel(), &ai.Context{
		Messages: ai.MessageList{tfpUserMsg("hi", js.DateNow())},
	}, nil))
	if !slices.Equal(tfpEventTypes(e1), []string{"start", "text_start", "text_delta", "text_end", "error"}) {
		t.Fatalf("unexpected explicit error events: %v", tfpEventTypes(e1))
	}
	if term, ok := e1[len(e1)-1].(*ai.AssistantMessageEventError); !ok || term.Reason != ai.StopReasonError || *term.Error.ErrorMessage != "upstream failed" {
		t.Fatalf("unexpected terminal error event: %+v", e1[len(e1)-1])
	}

	errMsg2 := providers.FauxAssistantMessage(providers.FauxAssistantText("partial"), nil)
	errMsg2.StopReason = ai.StopReasonAborted
	errMsg2.ErrorMessage = tfpPtr("Request was aborted")
	reg.SetResponses([]providers.FauxResponseStep{providers.FauxResponseMessage{Message: errMsg2}})
	e2 := tfpCollectEvents(t, compat.Stream(context.Background(), reg.GetModel(), &ai.Context{
		Messages: ai.MessageList{tfpUserMsg("hi", js.DateNow())},
	}, nil))
	if !slices.Equal(tfpEventTypes(e2), []string{"start", "text_start", "text_delta", "text_end", "error"}) {
		t.Fatalf("unexpected explicit aborted events: %v", tfpEventTypes(e2))
	}
	if term, ok := e2[len(e2)-1].(*ai.AssistantMessageEventError); !ok || term.Reason != ai.StopReasonAborted || *term.Error.ErrorMessage != "Request was aborted" {
		t.Fatalf("unexpected terminal aborted event: %+v", e2[len(e2)-1])
	}

	// Abort before first chunk
	tps50 := float64(50)
	s3 := 3
	regPreAbort := tfpRegister(t, &providers.RegisterFauxProviderOptions{
		TokensPerSecond: &tps50,
		TokenSize:       &providers.FauxTokenSizeOptions{Min: &s3, Max: &s3},
	})
	regPreAbort.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("abcdefghijklmnopqrstuvwxyz"), nil)},
	})
	ctxPre, cancelPre := context.WithCancel(context.Background())
	cancelPre()
	preEvs := tfpCollectEvents(t, compat.Stream(ctxPre, regPreAbort.GetModel(), &ai.Context{
		Messages: ai.MessageList{tfpUserMsg("hi", js.DateNow())},
	}, nil))
	if len(preEvs) != 1 {
		t.Fatalf("expected 1 pre-abort event, got %v", tfpEventTypes(preEvs))
	}
	if term, ok := preEvs[0].(*ai.AssistantMessageEventError); !ok || term.Reason != ai.StopReasonAborted || term.Error.StopReason != ai.StopReasonAborted {
		t.Fatalf("unexpected pre-abort event: %+v", preEvs[0])
	}

	// Abort mid-text, mid-thinking, mid-toolcall
	tps100 := float64(100)
	regMid := tfpRegister(t, &providers.RegisterFauxProviderOptions{
		TokensPerSecond: &tps100,
		TokenSize:       &providers.FauxTokenSizeOptions{Min: &s3, Max: &s3},
	})

	// mid-text
	regMid.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("abcdefghijklmnopqrstuvwxyz"), nil)},
	})
	ctxText, cancelText := context.WithCancel(context.Background())
	defer cancelText()
	var textTypes []string
	textDeltas := 0
	for ev := range compat.Stream(ctxText, regMid.GetModel(), &ai.Context{Messages: ai.MessageList{tfpUserMsg("hi", js.DateNow())}}, nil).All() {
		textTypes = append(textTypes, tfpEventType(ev))
		if _, ok := ev.(*ai.AssistantMessageEventTextDelta); ok {
			textDeltas++
			cancelText()
		}
	}
	if textDeltas != 1 || !slices.Contains(textTypes, "error") || slices.Contains(textTypes, "text_end") {
		t.Fatalf("mid-text abort types=%v deltas=%d", textTypes, textDeltas)
	}

	// mid-thinking
	regMid.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlock{Block: providers.FauxThinking("abcdefghijklmnopqrstuvwxyz")}, nil)},
	})
	ctxTh, cancelTh := context.WithCancel(context.Background())
	defer cancelTh()
	var thTypes []string
	thDeltas := 0
	for ev := range compat.Stream(ctxTh, regMid.GetModel(), &ai.Context{Messages: ai.MessageList{tfpUserMsg("hi", js.DateNow())}}, nil).All() {
		thTypes = append(thTypes, tfpEventType(ev))
		if _, ok := ev.(*ai.AssistantMessageEventThinkingDelta); ok {
			thDeltas++
			cancelTh()
		}
	}
	if thDeltas != 1 || !slices.Contains(thTypes, "error") || slices.Contains(thTypes, "thinking_end") {
		t.Fatalf("mid-thinking abort types=%v deltas=%d", thTypes, thDeltas)
	}

	// mid-toolcall
	bigArgs := jsonx.NewObject()
	bigArgs.Set("text", "abcdefghijklmnopqrstuvwxyz")
	bigArgs.Set("count", 123456789)
	toolStop := ai.StopReasonToolUse
	regMid.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(
				providers.FauxAssistantBlock{Block: providers.FauxToolCall("echo", bigArgs, &providers.FauxToolCallOptions{Id: tfpPtr("tool-1")})},
				&providers.FauxAssistantMessageOptions{StopReason: &toolStop},
			),
		},
	})
	ctxTool, cancelTool := context.WithCancel(context.Background())
	defer cancelTool()
	var toolTypes []string
	toolDeltas := 0
	for ev := range compat.Stream(ctxTool, regMid.GetModel(), &ai.Context{Messages: ai.MessageList{tfpUserMsg("hi", js.DateNow())}}, nil).All() {
		toolTypes = append(toolTypes, tfpEventType(ev))
		if _, ok := ev.(*ai.AssistantMessageEventToolcallDelta); ok {
			toolDeltas++
			cancelTool()
		}
	}
	if toolDeltas != 1 || !slices.Contains(toolTypes, "error") || slices.Contains(toolTypes, "toolcall_end") {
		t.Fatalf("mid-toolcall abort types=%v deltas=%d", toolTypes, toolDeltas)
	}
}

func TestFauxProviderCompat_Unregister(t *testing.T) {
	reg := compat.RegisterFauxProvider(nil)
	reg.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("hello"), nil)},
	})
	reg.Unregister()

	_, err := compat.Complete(context.Background(), reg.GetModel(), &ai.Context{
		Messages: ai.MessageList{tfpUserMsg("hi", js.DateNow())},
	}, nil)
	want := "No API provider registered for api: " + reg.Api
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("expected error %q, got %v", want, err)
	}
}
