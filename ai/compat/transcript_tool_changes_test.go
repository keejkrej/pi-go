// Ported from packages/ai/test/transcript-tool-changes.test.ts (pi v1.5.2).

package compat_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/compat"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

var tttcErrCaptured = errors.New("payload captured")

func tttcPtr[T any](v T) *T { return &v }

func tttcTool(name string, desc ...string) *ai.Tool {
	d := name + " tool"
	if len(desc) > 0 {
		d = desc[0]
	}
	return &ai.Tool{
		Name:        name,
		Description: d,
		Parameters:  typebox.Object(omap.NewMap[string, *typebox.Schema]()),
	}
}

func tttcCapturePayload(t *testing.T, model *ai.Model, c *ai.Context) *jsonx.Object {
	t.Helper()
	var captured *jsonx.Object
	apiKey := "test-key"
	s := compat.StreamSimple(context.Background(), model, c, &ai.SimpleStreamOptions{
		StreamOptions: ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
				ApiKey: &apiKey,
				OnPayload: func(payload any, _ *ai.Model) (any, error) {
					if obj, ok := payload.(*jsonx.Object); ok {
						captured = obj
					}
					return nil, tttcErrCaptured
				},
			},
		},
	})
	_, _ = s.Result(ai.OperationSignal(context.Background()))
	if captured == nil {
		t.Fatalf("expected payload capture for %s/%s", model.Provider, model.Id)
	}
	return captured
}

func tttcCaptureToolHistory(t *testing.T, model *ai.Model) []*jsonx.Object {
	t.Helper()
	baseTool := tttcTool("base_tool")
	dropTool := tttcTool("drop_tool")
	lateTool := tttcTool("late_tool")

	steps := [][]ai.Message{
		{&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tttcPtr("first")}, Timestamp: 1}},
		{
			&ai.SystemMessage{Role: "system", Content: ai.SystemMessageContent{Text: tttcPtr("")}, ToolsRemoved: []ai.ToolReference{{Name: "drop_tool"}}, Timestamp: 2},
			&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tttcPtr("after removal")}, Timestamp: 3},
		},
		{
			&ai.SystemMessage{Role: "system", Content: ai.SystemMessageContent{Text: tttcPtr("")}, ToolsAdded: []*ai.Tool{lateTool}, Timestamp: 4},
			&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tttcPtr("after first load")}, Timestamp: 5},
		},
		{
			&ai.SystemMessage{Role: "system", Content: ai.SystemMessageContent{Text: tttcPtr("")}, ToolsAdded: []*ai.Tool{tttcTool("later_tool")}, Timestamp: 6},
			&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tttcPtr("after second load")}, Timestamp: 7},
		},
	}

	messages := ai.MessageList{
		&ai.SystemMessage{
			Role:       "system",
			Content:    ai.SystemMessageContent{Text: tttcPtr("base prompt")},
			ToolsAdded: []*ai.Tool{baseTool, dropTool},
			Timestamp:  0,
		},
	}
	var payloads []*jsonx.Object
	for _, step := range steps {
		messages = append(messages, step...)
		cp := make(ai.MessageList, len(messages))
		copy(cp, messages)
		payloads = append(payloads, tttcCapturePayload(t, model, &ai.Context{Messages: cp}))
	}
	return payloads
}

func tttcExpectGrowingPrefix(t *testing.T, seqs [][]any) {
	t.Helper()
	for i := 1; i < len(seqs); i++ {
		prev := seqs[i-1]
		cur := seqs[i]
		if len(cur) < len(prev) {
			t.Fatalf("sequence[%d] len %d < prev len %d", i, len(cur), len(prev))
		}
		pb, err1 := jsonx.Marshal(prev)
		cb, err2 := jsonx.Marshal(cur[:len(prev)])
		if err1 != nil || err2 != nil {
			t.Fatalf("marshal err: %v / %v", err1, err2)
		}
		if string(pb) != string(cb) {
			t.Fatalf("prefix mismatch at step %d:\nprev: %s\ncur:  %s", i, pb, cb)
		}
	}
}

func tttcObj(t *testing.T, v any) *jsonx.Object {
	t.Helper()
	o, ok := v.(*jsonx.Object)
	if !ok || o == nil {
		t.Fatalf("expected *jsonx.Object, got %T", v)
	}
	return o
}

func tttcArr(t *testing.T, v any) []any {
	t.Helper()
	if a, ok := v.([]any); ok {
		return a
	}
	rv := reflect.ValueOf(v)
	if rv.IsValid() && rv.Kind() == reflect.Slice {
		out := make([]any, rv.Len())
		for i := range rv.Len() {
			out[i] = rv.Index(i).Interface()
		}
		return out
	}
	t.Fatalf("expected slice, got %T", v)
	return nil
}

func tttcGetArrField(t *testing.T, obj *jsonx.Object, key string) []any {
	t.Helper()
	v, ok := obj.Get(key)
	if !ok {
		t.Fatalf("missing field %q", key)
	}
	return tttcArr(t, v)
}

func tttcFieldToolNames(t *testing.T, tools []any, openaiFunction bool) []string {
	t.Helper()
	names := make([]string, 0, len(tools))
	for _, item := range tools {
		o := tttcObj(t, item)
		if openaiFunction {
			fnRaw, _ := o.Get("function")
			fn := tttcObj(t, fnRaw)
			n, _ := fn.Get("name")
			names = append(names, n.(string))
		} else {
			n, _ := o.Get("name")
			names = append(names, n.(string))
		}
	}
	return names
}

func tttcModelBase(id, name, apiName, provider string, compatVal ai.ModelCompat) *ai.Model {
	var c ai.ModelCompatValue
	if compatVal != nil {
		c = ai.ModelCompatValue{V: compatVal}
	}
	return &ai.Model{
		BaseModel: ai.BaseModel{
			Id:       id,
			Name:     name,
			Api:      apiName,
			Provider: ai.ProviderId(provider),
			BaseUrl:  "http://127.0.0.1:9",
			Input:    []ai.ModelModality{ai.ModelModalityText},
		},
		Reasoning:     true,
		ContextWindow: 100000,
		MaxTokens:     1000,
		Compat:        c,
	}
}

func tttcSharedContexts() (*ai.Context, *ai.Context) {
	baseTool := tttcTool("base_tool")
	lateTool := tttcTool("late_tool")

	secs0 := omap.NewMap[string, *string]()
	oldRules := "<rules>\nold rules\n</rules>"
	readDocs := "<docs>\nread docs\n</docs>"
	secs0.Set("rules", &oldRules)
	secs0.Set("docs", &readDocs)

	secs2 := omap.NewMap[string, *string]()
	newRules := "<rules>\nnew rules\n</rules>"
	secs2.Set("rules", &newRules)
	secs2.Set("docs", nil)

	ctx := &ai.Context{
		Messages: ai.MessageList{
			&ai.SystemMessage{
				Role:       "system",
				Content:    ai.SystemMessageContent{Text: tttcPtr("base prompt")},
				Sections:   secs0,
				ToolsAdded: []*ai.Tool{baseTool},
				Timestamp:  0,
			},
			&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tttcPtr("before")}, Timestamp: 1},
			&ai.SystemMessage{
				Role:         "system",
				Content:      ai.SystemMessageContent{Text: tttcPtr("updated guidance")},
				Sections:     secs2,
				ToolsRemoved: []ai.ToolReference{{Name: "base_tool"}},
				ToolsAdded:   []*ai.Tool{lateTool},
				Timestamp:    2,
			},
		},
	}

	addCtx := &ai.Context{
		Messages: ai.MessageList{
			&ai.SystemMessage{
				Role:       "system",
				Content:    ai.SystemMessageContent{Text: tttcPtr("base prompt")},
				ToolsAdded: []*ai.Tool{baseTool},
				Timestamp:  0,
			},
			&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tttcPtr("before")}, Timestamp: 1},
			&ai.SystemMessage{
				Role:       "system",
				Content:    ai.SystemMessageContent{Text: tttcPtr("updated guidance")},
				ToolsAdded: []*ai.Tool{lateTool},
				Timestamp:  2,
			},
		},
	}
	return ctx, addCtx
}

func TestTranscriptToolChanges(t *testing.T) {
	sharedCtx, additionCtx := tttcSharedContexts()
	yes := true

	anthropicNative := tttcModelBase("claude-opus-5", "Claude Opus 5", string(ai.KnownApiAnthropicMessages), "anthropic", &ai.AnthropicMessagesCompat{
		SupportsMidConvoSystemMessages: &yes,
		SupportsMidConvoToolChanges:    &yes,
	})
	additionalToolsModel := tttcModelBase("gpt-5.4", "GPT-5.4", string(ai.KnownApiOpenaiResponses), "openai", &ai.OpenAIResponsesCompat{
		SupportsMidConvoSystemMessages: &yes,
		SupportsAdditionalTools:        &yes,
	})

	t.Run("sends Anthropic updates and tool changes in native system messages", func(t *testing.T) {
		payload := tttcCapturePayload(t, anthropicNative, sharedCtx)

		betas := tttcGetArrField(t, payload, "betas")
		var betaStrs []string
		for _, b := range betas {
			betaStrs = append(betaStrs, b.(string))
		}
		if !slices.Contains(betaStrs, "inline-tools-2026-09-15") {
			t.Fatalf("expected inline-tools-2026-09-15 in betas: %v", betaStrs)
		}
		if slices.Contains(betaStrs, "mid-conversation-tool-changes-2026-07-01") {
			t.Fatalf("unexpected legacy mid-conversation-tool-changes beta: %v", betaStrs)
		}

		sysBlocks := tttcGetArrField(t, payload, "system")
		if len(sysBlocks) != 1 {
			t.Fatalf("system len = %d, want 1", len(sysBlocks))
		}
		if txt, _ := tttcObj(t, sysBlocks[0]).Get("text"); txt != "base prompt\n\n<rules>\nold rules\n</rules>\n\n<docs>\nread docs\n</docs>" {
			t.Fatalf("system[0].text = %q", txt)
		}

		tools := tttcGetArrField(t, payload, "tools")
		if len(tools) != 2 {
			t.Fatalf("tools len = %d, want 2", len(tools))
		}
		t0, t1 := tttcObj(t, tools[0]), tttcObj(t, tools[1])
		if n, _ := t0.Get("name"); n != "base_tool" {
			t.Fatalf("tools[0].name = %v", n)
		}
		if ccRaw, ok := t0.Get("cache_control"); !ok || tttcObj(t, ccRaw) == nil {
			t.Fatal("expected cache_control on tools[0]")
		}
		if _, has := t0.Get("defer_loading"); has {
			t.Fatal("unexpected defer_loading on tools[0]")
		}
		if n, _ := t1.Get("name"); n != "__pi_deferred_placeholder__" {
			t.Fatalf("tools[1].name = %v", n)
		}
		if dl, _ := t1.Get("defer_loading"); dl != true {
			t.Fatalf("tools[1].defer_loading = %v", dl)
		}
		if _, has := t1.Get("cache_control"); has {
			t.Fatal("unexpected cache_control on placeholder tool")
		}

		msgs := tttcGetArrField(t, payload, "messages")
		update := tttcObj(t, msgs[len(msgs)-1])
		if role, _ := update.Get("role"); role != "system" {
			t.Fatalf("last message role = %v, want system", role)
		}
		uContent := tttcGetArrField(t, update, "content")
		if len(uContent) != 3 {
			t.Fatalf("update content len = %d, want 3", len(uContent))
		}
		b0, b1, b2 := tttcObj(t, uContent[0]), tttcObj(t, uContent[1]), tttcObj(t, uContent[2])
		b0Text, _ := b0.Get("text")
		if txt, _ := b0Text.(string); !strings.Contains(txt, "updated guidance") || !strings.Contains(txt, "<rules>\nnew rules\n</rules>") || !strings.Contains(txt, `Removed system prompt section "docs"`) {
			t.Fatalf("unexpected update text: %q", txt)
		}
		if typ, _ := b1.Get("type"); typ != "tool_removal" {
			t.Fatalf("b1.type = %v", typ)
		}
		b1ToolRaw, _ := b1.Get("tool")
		if n, _ := tttcObj(t, b1ToolRaw).Get("name"); n != "base_tool" {
			t.Fatalf("b1.tool.name = %v", n)
		}
		if typ, _ := b2.Get("type"); typ != "tool_addition" {
			t.Fatalf("b2.type = %v", typ)
		}
		b2ToolRaw, _ := b2.Get("tool")
		b2DefRaw, _ := tttcObj(t, b2ToolRaw).Get("definition")
		b2Def := tttcObj(t, b2DefRaw)
		if n, _ := b2Def.Get("name"); n != "late_tool" {
			t.Fatalf("b2.definition.name = %v", n)
		}
		if _, has := b2Def.Get("cache_control"); has {
			t.Fatal("unexpected cache_control on inline tool definition")
		}
		if _, has := b2Def.Get("defer_loading"); has {
			t.Fatal("unexpected defer_loading on inline tool definition")
		}

		initial := tttcCapturePayload(t, anthropicNative, &ai.Context{Messages: sharedCtx.Messages[:2]})
		initNames := tttcFieldToolNames(t, tttcGetArrField(t, initial, "tools"), false)
		if !slices.Equal(initNames, []string{"base_tool", "__pi_deferred_placeholder__"}) {
			t.Fatalf("initial tools = %v", initNames)
		}
	})

	t.Run("redefines an Anthropic tool by value under the same name", func(t *testing.T) {
		baseTool := tttcTool("base_tool")
		redefined := tttcTool("base_tool", "changed")
		payload := tttcCapturePayload(t, anthropicNative, &ai.Context{
			Messages: ai.MessageList{
				&ai.SystemMessage{Role: "system", Content: ai.SystemMessageContent{Text: tttcPtr("base prompt")}, ToolsAdded: []*ai.Tool{baseTool}, Timestamp: 0},
				&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tttcPtr("before")}, Timestamp: 1},
				&ai.SystemMessage{Role: "system", Content: ai.SystemMessageContent{Text: tttcPtr("")}, ToolsRemoved: []ai.ToolReference{{Name: "base_tool"}}, ToolsAdded: []*ai.Tool{redefined}, Timestamp: 2},
			},
		})
		tools := tttcGetArrField(t, payload, "tools")
		if d, _ := tttcObj(t, tools[0]).Get("description"); d != "base_tool tool" {
			t.Fatalf("top-level tool description = %v", d)
		}
		msgs := tttcGetArrField(t, payload, "messages")
		last := tttcObj(t, msgs[len(msgs)-1])
		content := tttcGetArrField(t, last, "content")
		if len(content) != 1 {
			t.Fatalf("redefine content len = %d, want 1", len(content))
		}
		b0 := tttcObj(t, content[0])
		if typ, _ := b0.Get("type"); typ != "tool_addition" {
			t.Fatalf("b0.type = %v, want tool_addition", typ)
		}
		b0ToolRaw, _ := b0.Get("tool")
		b0DefRaw, _ := tttcObj(t, b0ToolRaw).Get("definition")
		if d, _ := tttcObj(t, b0DefRaw).Get("description"); d != "changed" {
			t.Fatalf("redefine description = %v, want changed", d)
		}
	})

	t.Run("sends the current Anthropic tool list without an initial tool", func(t *testing.T) {
		payload := tttcCapturePayload(t, anthropicNative, &ai.Context{
			Messages: ai.MessageList{
				&ai.SystemMessage{Role: "system", Content: ai.SystemMessageContent{Text: tttcPtr("base prompt")}, Timestamp: 0},
				&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tttcPtr("before")}, Timestamp: 1},
				&ai.SystemMessage{Role: "system", Content: ai.SystemMessageContent{Text: tttcPtr("updated guidance")}, ToolsAdded: []*ai.Tool{tttcTool("late_tool")}, Timestamp: 2},
			},
		})
		if betasRaw, ok := payload.Get("betas"); ok {
			for _, b := range tttcArr(t, betasRaw) {
				if b == "inline-tools-2026-09-15" {
					t.Fatal("unexpected inline-tools beta without initial tool")
				}
			}
		}
		tools := tttcGetArrField(t, payload, "tools")
		if names := tttcFieldToolNames(t, tools, false); !slices.Equal(names, []string{"late_tool"}) {
			t.Fatalf("tools = %v, want [late_tool]", names)
		}
		msgs := tttcGetArrField(t, payload, "messages")
		lastContent := tttcGetArrField(t, tttcObj(t, msgs[len(msgs)-1]), "content")
		if len(lastContent) != 1 || func() string { v, _ := tttcObj(t, lastContent[0]).Get("type"); return v.(string) }() != "text" {
			t.Fatalf("expected only [text] in last update content")
		}
	})

	t.Run("holds an Anthropic update past an assistant message that converts to no blocks (#11091)", func(t *testing.T) {
		reply := func(text, provider, apiName, modelId string) *ai.AssistantMessage {
			return &ai.AssistantMessage{
				Role:       "assistant",
				Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: text}},
				Api:        ai.Api(apiName),
				Provider:   ai.ProviderId(provider),
				Model:      modelId,
				StopReason: ai.StopReasonStop,
				Timestamp:  3,
			}
		}
		msgs := ai.MessageList{
			&ai.SystemMessage{Role: "system", Content: ai.SystemMessageContent{Text: tttcPtr("base prompt")}, Timestamp: 0},
			&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tttcPtr("hi")}, Timestamp: 1},
			&ai.SystemMessage{Role: "system", Content: ai.SystemMessageContent{Text: tttcPtr("updated guidance")}, Timestamp: 2},
			reply("", "openai-codex", "openai-codex-responses", "gpt-x"),
			&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tttcPtr("again")}, Timestamp: 4},
		}
		roles := func(p *jsonx.Object) []string {
			arr := tttcGetArrField(t, p, "messages")
			out := make([]string, len(arr))
			for i, item := range arr {
				r, _ := tttcObj(t, item).Get("role")
				out[i] = r.(string)
			}
			return out
		}

		atEnd := tttcCapturePayload(t, anthropicNative, &ai.Context{Messages: msgs})
		if got := roles(atEnd); !slices.Equal(got, []string{"user", "user", "system"}) {
			t.Fatalf("atEnd roles = %v", got)
		}

		msgsWithReply := make(ai.MessageList, 0, len(msgs)+2)
		msgsWithReply = append(msgsWithReply, msgs...)
		msgsWithReply = append(msgsWithReply,
			reply("answer", string(anthropicNative.Provider), anthropicNative.Api, anthropicNative.Id),
			&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tttcPtr("third")}, Timestamp: 5},
		)
		beforeAssistant := tttcCapturePayload(t, anthropicNative, &ai.Context{Messages: msgsWithReply})
		if got := roles(beforeAssistant); !slices.Equal(got, []string{"user", "user", "system", "assistant", "user"}) {
			t.Fatalf("beforeAssistant roles = %v", got)
		}
	})

	t.Run("folds Anthropic updates into the system prompt without native support", func(t *testing.T) {
		model := tttcModelBase("claude-sonnet-4-5", "Claude Sonnet 4.5", string(ai.KnownApiAnthropicMessages), "anthropic", nil)
		payload := tttcCapturePayload(t, model, sharedCtx)
		sys := tttcGetArrField(t, payload, "system")
		if len(sys) != 1 {
			t.Fatalf("system len = %d", len(sys))
		}
		if txt, _ := tttcObj(t, sys[0]).Get("text"); txt != "base prompt\n\nupdated guidance\n\n<rules>\nnew rules\n</rules>" {
			t.Fatalf("folded system text = %q", txt)
		}
		if names := tttcFieldToolNames(t, tttcGetArrField(t, payload, "tools"), false); !slices.Equal(names, []string{"late_tool"}) {
			t.Fatalf("tools = %v", names)
		}
		msgs := tttcGetArrField(t, payload, "messages")
		if len(msgs) != 1 || func() string { r, _ := tttcObj(t, msgs[0]).Get("role"); return r.(string) }() != "user" {
			t.Fatalf("expected single user message, got %d", len(msgs))
		}
	})

	t.Run("requires both Anthropic capabilities for native tool changes", func(t *testing.T) {
		model := tttcModelBase("claude-opus-5", "Claude Opus 5", string(ai.KnownApiAnthropicMessages), "anthropic", &ai.AnthropicMessagesCompat{
			SupportsMidConvoToolChanges: &yes,
		})
		payload := tttcCapturePayload(t, model, sharedCtx)
		if names := tttcFieldToolNames(t, tttcGetArrField(t, payload, "tools"), false); !slices.Equal(names, []string{"late_tool"}) {
			t.Fatalf("tools = %v", names)
		}
		msgs := tttcGetArrField(t, payload, "messages")
		if len(msgs) != 1 || func() string { r, _ := tttcObj(t, msgs[0]).Get("role"); return r.(string) }() != "user" {
			t.Fatalf("expected single user message")
		}
	})

	t.Run("declares every OpenAI tool in additional_tools at its developer message", func(t *testing.T) {
		payload := tttcCapturePayload(t, additionalToolsModel, additionCtx)
		if _, has := payload.Get("tools"); has {
			t.Fatal("expected top-level tools to be omitted")
		}
		input := tttcGetArrField(t, payload, "input")
		var addNames []string
		for _, item := range input {
			o := tttcObj(t, item)
			if typ, _ := o.Get("type"); typ == "additional_tools" {
				tList := tttcGetArrField(t, o, "tools")
				n, _ := tttcObj(t, tList[0]).Get("name")
				addNames = append(addNames, n.(string))
			}
		}
		if !slices.Equal(addNames, []string{"base_tool", "late_tool"}) {
			t.Fatalf("additional_tools names = %v", addNames)
		}
		if len(input) != 6 {
			t.Fatalf("input len = %d, want 6", len(input))
		}
		if c, _ := tttcObj(t, input[1]).Get("content"); c != "base prompt" {
			t.Fatalf("input[1].content = %v", c)
		}
		if c, _ := tttcObj(t, input[3]).Get("content"); !strings.Contains(c.(string), "incremental tools update") {
			t.Fatalf("input[3].content = %v", c)
		}
		if c, _ := tttcObj(t, input[5]).Get("content"); c != "updated guidance" {
			t.Fatalf("input[5].content = %v", c)
		}
	})

	t.Run("maps system-message additions into synthetic tool search", func(t *testing.T) {
		model := tttcModelBase("gpt-5.4", "GPT-5.4", string(ai.KnownApiOpenaiResponses), "openai", &ai.OpenAIResponsesCompat{
			SupportsMidConvoSystemMessages: &yes,
			SupportsToolSearch:             &yes,
		})
		payload := tttcCapturePayload(t, model, additionCtx)
		if names := tttcFieldToolNames(t, tttcGetArrField(t, payload, "tools"), false); !slices.Equal(names, []string{"base_tool"}) {
			t.Fatalf("tools = %v", names)
		}
		input := tttcGetArrField(t, payload, "input")
		var sawSearchCall bool
		var searchOutTools []string
		for _, item := range input {
			o := tttcObj(t, item)
			typ, _ := o.Get("type")
			if typ == "tool_search_call" {
				sawSearchCall = true
			}
			if typ == "tool_search_output" {
				searchOutTools = tttcFieldToolNames(t, tttcGetArrField(t, o, "tools"), false)
			}
		}
		if !sawSearchCall {
			t.Fatal("expected tool_search_call in input")
		}
		if !slices.Equal(searchOutTools, []string{"late_tool"}) {
			t.Fatalf("tool_search_output tools = %v", searchOutTools)
		}
	})

	t.Run("folds OpenAI updates into the leading developer message without native support", func(t *testing.T) {
		model := tttcModelBase("gpt-4.1", "GPT-4.1", string(ai.KnownApiOpenaiResponses), "openai", &ai.OpenAIResponsesCompat{
			SupportsAdditionalTools: &yes,
		})
		payload := tttcCapturePayload(t, model, sharedCtx)
		if names := tttcFieldToolNames(t, tttcGetArrField(t, payload, "tools"), false); !slices.Equal(names, []string{"late_tool"}) {
			t.Fatalf("tools = %v", names)
		}
		input := tttcGetArrField(t, payload, "input")
		if len(input) != 2 {
			t.Fatalf("input len = %d, want 2", len(input))
		}
		if c, _ := tttcObj(t, input[0]).Get("content"); c != "base prompt\n\nupdated guidance\n\n<rules>\nnew rules\n</rules>" {
			t.Fatalf("folded developer content = %q", c)
		}
	})

	t.Run("announces OpenAI removals as text", func(t *testing.T) {
		payload := tttcCapturePayload(t, additionalToolsModel, sharedCtx)
		if _, has := payload.Get("tools"); has {
			t.Fatal("expected top-level tools omitted")
		}
		input := tttcGetArrField(t, payload, "input")
		lastContent, _ := tttcObj(t, input[len(input)-1]).Get("content")
		s, _ := lastContent.(string)
		if !strings.Contains(s, "updated guidance") || !strings.Contains(s, "The following tools are no longer available. Do not call them:\n- base_tool") {
			t.Fatalf("unexpected removal announcement: %q", s)
		}
	})

	t.Run("redefines an OpenAI tool with additional_tools instead of announcing a removal", func(t *testing.T) {
		baseTool := tttcTool("base_tool")
		payload := tttcCapturePayload(t, additionalToolsModel, &ai.Context{
			Messages: ai.MessageList{
				&ai.SystemMessage{Role: "system", Content: ai.SystemMessageContent{Text: tttcPtr("base prompt")}, ToolsAdded: []*ai.Tool{baseTool}, Timestamp: 0},
				&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tttcPtr("before")}, Timestamp: 1},
				&ai.SystemMessage{
					Role:         "system",
					Content:      ai.SystemMessageContent{Text: tttcPtr("")},
					ToolsRemoved: []ai.ToolReference{{Name: "base_tool"}},
					ToolsAdded:   []*ai.Tool{tttcTool("base_tool", "changed")},
					Timestamp:    2,
				},
			},
		})
		if _, has := payload.Get("tools"); has {
			t.Fatal("expected top-level tools omitted")
		}
		input := tttcGetArrField(t, payload, "input")
		if len(input) != 5 {
			t.Fatalf("input len = %d, want 5", len(input))
		}
		if c, _ := tttcObj(t, input[3]).Get("content"); !strings.Contains(c.(string), "latest definition replaces the earlier one") {
			t.Fatalf("input[3].content = %v", c)
		}
		lastTools := tttcGetArrField(t, tttcObj(t, input[4]), "tools")
		if d, _ := tttcObj(t, lastTools[0]).Get("description"); d != "changed" {
			t.Fatalf("redefined description = %v", d)
		}
	})

	t.Run("keeps the OpenAI request prefix across a removal and later loads (#10940)", func(t *testing.T) {
		payloads := tttcCaptureToolHistory(t, additionalToolsModel)
		inputs := make([][]any, len(payloads))
		for i, p := range payloads {
			if _, has := p.Get("tools"); has {
				t.Fatalf("payload[%d] unexpected top-level tools", i)
			}
			inputs[i] = tttcGetArrField(t, p, "input")
		}
		tttcExpectGrowingPrefix(t, inputs)

		lastInput := inputs[len(inputs)-1]
		var stillUnavailCount int
		var sawDropRemoval bool
		for _, item := range lastInput {
			o := tttcObj(t, item)
			if c, ok := o.Get("content"); ok {
				if s, ok := c.(string); ok {
					if strings.Contains(s, "no longer available. Do not call them:\n- drop_tool") {
						sawDropRemoval = true
					}
					if strings.Contains(s, "incremental tools update") && strings.Contains(s, "\nStill unavailable: drop_tool.") {
						stillUnavailCount++
					}
				}
			}
		}
		if !sawDropRemoval {
			t.Fatal("expected drop_tool removal notice")
		}
		if stillUnavailCount != 2 {
			t.Fatalf("stillUnavailCount = %d, want 2", stillUnavailCount)
		}
	})

	t.Run("stops listing a tool as unavailable once it is declared again", func(t *testing.T) {
		baseTool := tttcTool("base_tool")
		dropTool := tttcTool("drop_tool")
		payload := tttcCapturePayload(t, additionalToolsModel, &ai.Context{
			Messages: ai.MessageList{
				&ai.SystemMessage{Role: "system", Content: ai.SystemMessageContent{Text: tttcPtr("base prompt")}, ToolsAdded: []*ai.Tool{baseTool, dropTool}, Timestamp: 0},
				&ai.SystemMessage{Role: "system", Content: ai.SystemMessageContent{Text: tttcPtr("")}, ToolsRemoved: []ai.ToolReference{{Name: "drop_tool"}}, Timestamp: 1},
				&ai.SystemMessage{Role: "system", Content: ai.SystemMessageContent{Text: tttcPtr("")}, ToolsAdded: []*ai.Tool{dropTool}, Timestamp: 2},
				&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tttcPtr("go")}, Timestamp: 3},
			},
		})
		input := tttcGetArrField(t, payload, "input")
		var notice string
		for _, item := range input {
			if c, ok := tttcObj(t, item).Get("content"); ok {
				if s, ok := c.(string); ok && strings.Contains(s, "incremental tools update") {
					notice = s
					break
				}
			}
		}
		if notice == "" {
			t.Fatal("expected incremental tools update notice")
		}
		if strings.Contains(notice, "Still unavailable") {
			t.Fatalf("unexpected Still unavailable in notice: %q", notice)
		}
		redecl := tttcObj(t, input[len(input)-2])
		if typ, _ := redecl.Get("type"); typ != "additional_tools" {
			t.Fatalf("input[-2].type = %v", typ)
		}
		if names := tttcFieldToolNames(t, tttcGetArrField(t, redecl, "tools"), false); !slices.Equal(names, []string{"drop_tool"}) {
			t.Fatalf("input[-2].tools = %v", names)
		}
	})

	t.Run("does not list a tool as still unavailable before its removal is announced", func(t *testing.T) {
		payload := tttcCapturePayload(t, additionalToolsModel, &ai.Context{
			Messages: ai.MessageList{
				&ai.SystemMessage{Role: "system", Content: ai.SystemMessageContent{Text: tttcPtr("base prompt")}, ToolsAdded: []*ai.Tool{tttcTool("base_tool"), tttcTool("drop_tool")}, Timestamp: 0},
				&ai.SystemMessage{Role: "system", Content: ai.SystemMessageContent{Text: tttcPtr("")}, ToolsAdded: []*ai.Tool{tttcTool("late_tool")}, ToolsRemoved: []ai.ToolReference{{Name: "drop_tool"}}, Timestamp: 1},
				&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tttcPtr("go")}, Timestamp: 2},
			},
		})
		input := tttcGetArrField(t, payload, "input")
		noticeObj := tttcObj(t, input[len(input)-4])
		notice, _ := noticeObj.Get("content")
		ns, _ := notice.(string)
		if !strings.Contains(ns, "incremental tools update") || strings.Contains(ns, "Still unavailable") {
			t.Fatalf("unexpected notice: %q", ns)
		}
		if names := tttcFieldToolNames(t, tttcGetArrField(t, tttcObj(t, input[len(input)-3]), "tools"), false); !slices.Equal(names, []string{"late_tool"}) {
			t.Fatalf("input[-3].tools = %v", names)
		}
		remContent, _ := tttcObj(t, input[len(input)-2]).Get("content")
		if s, _ := remContent.(string); !strings.Contains(s, "no longer available. Do not call them:\n- drop_tool") {
			t.Fatalf("input[-2].content = %q", s)
		}
	})

	t.Run("keeps the OpenAI tool search prefix across loads after a removal (#10940)", func(t *testing.T) {
		model := *additionalToolsModel
		model.Compat = ai.ModelCompatValue{V: &ai.OpenAIResponsesCompat{
			SupportsMidConvoSystemMessages: &yes,
			SupportsToolSearch:             &yes,
		}}
		payloads := tttcCaptureToolHistory(t, &model)
		beforeNames := tttcFieldToolNames(t, tttcGetArrField(t, payloads[0], "tools"), false)
		if !slices.Equal(beforeNames, []string{"base_tool", "drop_tool"}) {
			t.Fatalf("beforeRemoval tools = %v", beforeNames)
		}
		afterInputs := make([][]any, len(payloads)-1)
		for i, p := range payloads[1:] {
			if names := tttcFieldToolNames(t, tttcGetArrField(t, p, "tools"), false); !slices.Equal(names, []string{"base_tool"}) {
				t.Fatalf("afterRemoval[%d] tools = %v", i, names)
			}
			afterInputs[i] = tttcGetArrField(t, p, "input")
		}
		tttcExpectGrowingPrefix(t, afterInputs)
		var outNames []string
		for _, item := range afterInputs[len(afterInputs)-1] {
			o := tttcObj(t, item)
			if typ, _ := o.Get("type"); typ == "tool_search_output" {
				outNames = append(outNames, tttcFieldToolNames(t, tttcGetArrField(t, o, "tools"), false)...)
			}
		}
		if !slices.Equal(outNames, []string{"late_tool", "later_tool"}) {
			t.Fatalf("tool_search_output tools = %v", outNames)
		}
	})

	t.Run("resolves the top-level tool boundary per transport (#10940)", func(t *testing.T) {
		baseTool := tttcTool("base_tool")
		dropTool := tttcTool("drop_tool")
		lateX := tttcTool("late_tool", "x")
		lateY := tttcTool("late_tool", "y")
		msgs := []ai.Message{
			&ai.SystemMessage{Role: "system", Content: ai.SystemMessageContent{Text: tttcPtr("base prompt")}, ToolsAdded: []*ai.Tool{baseTool, dropTool}, Timestamp: 0},
			&ai.SystemMessage{Role: "system", Content: ai.SystemMessageContent{Text: tttcPtr("")}, ToolsRemoved: []ai.ToolReference{{Name: "drop_tool"}}, Timestamp: 1},
			&ai.SystemMessage{Role: "system", Content: ai.SystemMessageContent{Text: tttcPtr("")}, ToolsAdded: []*ai.Tool{dropTool}, Timestamp: 2},
			&ai.SystemMessage{Role: "system", Content: ai.SystemMessageContent{Text: tttcPtr("")}, ToolsAdded: []*ai.Tool{lateX}, Timestamp: 3},
			&ai.SystemMessage{Role: "system", Content: ai.SystemMessageContent{Text: tttcPtr("")}, ToolsAdded: []*ai.Tool{lateY}, Timestamp: 4},
		}
		toolNames := func(tt ai.TranscriptTools) []string {
			out := make([]string, len(tt.RequestTools))
			for i, tl := range tt.RequestTools {
				out[i] = tl.Name + ":" + tl.Description
			}
			return out
		}
		r1 := ai.ResolveTranscriptTools(msgs[:2], ai.TranscriptToolSupportAdditions)
		if r1.FoldedSystemMessages != 2 || !slices.Equal(toolNames(r1), []string{"base_tool:base_tool tool"}) {
			t.Fatalf("r1 = %+v", r1)
		}
		r2 := ai.ResolveTranscriptTools(msgs[:4], ai.TranscriptToolSupportAdditions)
		if r2.FoldedSystemMessages != 3 || !slices.Equal(toolNames(r2), []string{"base_tool:base_tool tool", "drop_tool:drop_tool tool"}) {
			t.Fatalf("r2 = %+v", r2)
		}
		r3 := ai.ResolveTranscriptTools(msgs, ai.TranscriptToolSupportAdditions)
		if r3.FoldedSystemMessages != 5 || !slices.Equal(toolNames(r3), []string{"base_tool:base_tool tool", "drop_tool:drop_tool tool", "late_tool:y"}) {
			t.Fatalf("r3 = %+v", r3)
		}
		rChanges := ai.ResolveTranscriptTools(msgs, ai.TranscriptToolSupportChanges)
		if rChanges.FoldedSystemMessages != 0 || len(rChanges.RequestTools) != 0 {
			t.Fatalf("rChanges = %+v", rChanges)
		}
		rNone := ai.ResolveTranscriptTools(msgs, ai.TranscriptToolSupportNone)
		if rNone.FoldedSystemMessages != 5 || !slices.Equal(toolNames(rNone), []string{"base_tool:base_tool tool", "drop_tool:drop_tool tool", "late_tool:y"}) {
			t.Fatalf("rNone = %+v", rNone)
		}
	})

	t.Run("anchors Kimi additions in tool-bearing system messages", func(t *testing.T) {
		model := tttcModelBase("kimi-k3", "Kimi K3", string(ai.KnownApiOpenaiCompletions), "moonshotai", &ai.OpenAICompletionsCompat{
			SupportsMidConvoSystemMessages: &yes,
			SupportsMidConvoToolAdditions:  &yes,
		})
		payload := tttcCapturePayload(t, model, additionCtx)
		if names := tttcFieldToolNames(t, tttcGetArrField(t, payload, "tools"), true); !slices.Equal(names, []string{"base_tool"}) {
			t.Fatalf("tools = %v", names)
		}
		msgs := tttcGetArrField(t, payload, "messages")
		var toolMsgNames []string
		var sysContents []string
		for _, item := range msgs {
			o := tttcObj(t, item)
			if tsRaw, ok := o.Get("tools"); ok && len(toolMsgNames) == 0 {
				toolMsgNames = tttcFieldToolNames(t, tttcArr(t, tsRaw), true)
			}
			if r, _ := o.Get("role"); r == "system" {
				if c, has := o.Get("content"); has && c != nil {
					sysContents = append(sysContents, c.(string))
				} else {
					sysContents = append(sysContents, "<nil>")
				}
			}
		}
		if !slices.Equal(toolMsgNames, []string{"late_tool"}) {
			t.Fatalf("toolMsgNames = %v", toolMsgNames)
		}
		if !slices.Equal(sysContents, []string{"base prompt", "<nil>", "updated guidance"}) {
			t.Fatalf("sysContents = %v", sysContents)
		}
	})

	t.Run("keeps the Kimi prefix across loads after a removal (#10940)", func(t *testing.T) {
		k3 := compat.GetModel("kimi-coding", "k3")
		if k3 == nil {
			t.Fatal("missing kimi-coding/k3")
		}
		payloads := tttcCaptureToolHistory(t, k3)
		if names := tttcFieldToolNames(t, tttcGetArrField(t, payloads[0], "tools"), true); !slices.Equal(names, []string{"base_tool", "drop_tool"}) {
			t.Fatalf("beforeRemoval tools = %v", names)
		}
		afterMsgs := make([][]any, len(payloads)-1)
		for i, p := range payloads[1:] {
			if names := tttcFieldToolNames(t, tttcGetArrField(t, p, "tools"), true); !slices.Equal(names, []string{"base_tool"}) {
				t.Fatalf("afterRemoval[%d] tools = %v", i, names)
			}
			afterMsgs[i] = tttcGetArrField(t, p, "messages")
		}
		tttcExpectGrowingPrefix(t, afterMsgs)
		var anchored []string
		for _, item := range afterMsgs[len(afterMsgs)-1] {
			o := tttcObj(t, item)
			if tsRaw, ok := o.Get("tools"); ok {
				anchored = append(anchored, tttcFieldToolNames(t, tttcArr(t, tsRaw), true)...)
			}
		}
		if !slices.Equal(anchored, []string{"late_tool", "later_tool"}) {
			t.Fatalf("anchored = %v", anchored)
		}
	})

	for _, modelId := range []string{"k3", "k3-256k", "kimi-for-coding"} {
		t.Run("anchors additions for Kimi Coding "+modelId+" (#10720)", func(t *testing.T) {
			m := compat.GetModel("kimi-coding", modelId)
			if m == nil {
				t.Fatalf("missing kimi-coding/%s", modelId)
			}
			payload := tttcCapturePayload(t, m, additionCtx)
			if names := tttcFieldToolNames(t, tttcGetArrField(t, payload, "tools"), true); !slices.Equal(names, []string{"base_tool"}) {
				t.Fatalf("tools = %v", names)
			}
			msgs := tttcGetArrField(t, payload, "messages")
			var anchored []string
			for _, item := range msgs {
				if tsRaw, ok := tttcObj(t, item).Get("tools"); ok {
					anchored = tttcFieldToolNames(t, tttcArr(t, tsRaw), true)
					break
				}
			}
			if !slices.Equal(anchored, []string{"late_tool"}) {
				t.Fatalf("anchored = %v", anchored)
			}
		})
	}

	t.Run("keeps Kimi Coding Highspeed additions in top-level tools (#10720)", func(t *testing.T) {
		m := compat.GetModel("kimi-coding", "kimi-for-coding-highspeed")
		if m == nil {
			t.Fatal("missing kimi-coding/kimi-for-coding-highspeed")
		}
		payload := tttcCapturePayload(t, m, additionCtx)
		if names := tttcFieldToolNames(t, tttcGetArrField(t, payload, "tools"), true); !slices.Equal(names, []string{"base_tool", "late_tool"}) {
			t.Fatalf("tools = %v", names)
		}
		for _, item := range tttcGetArrField(t, payload, "messages") {
			if _, has := tttcObj(t, item).Get("tools"); has {
				t.Fatal("unexpected tool-bearing system message on highspeed model")
			}
		}
	})

	t.Run("keeps Kimi K2 system text inline without dynamic tool messages", func(t *testing.T) {
		model := tttcModelBase("kimi-k2.7-code", "Kimi K2.7 Code", string(ai.KnownApiOpenaiCompletions), "moonshotai", &ai.OpenAICompletionsCompat{
			SupportsMidConvoSystemMessages: &yes,
		})
		payload := tttcCapturePayload(t, model, additionCtx)
		if names := tttcFieldToolNames(t, tttcGetArrField(t, payload, "tools"), true); !slices.Equal(names, []string{"base_tool", "late_tool"}) {
			t.Fatalf("tools = %v", names)
		}
		var sysContents []string
		for _, item := range tttcGetArrField(t, payload, "messages") {
			o := tttcObj(t, item)
			if _, has := o.Get("tools"); has {
				t.Fatal("unexpected message.tools on Kimi K2")
			}
			if r, _ := o.Get("role"); r == "system" {
				c, _ := o.Get("content")
				sysContents = append(sysContents, c.(string))
			}
		}
		if !slices.Equal(sysContents, []string{"base prompt", "updated guidance"}) {
			t.Fatalf("sysContents = %v", sysContents)
		}
	})

	t.Run("folds OpenAI-compatible updates into the system prompt without native support", func(t *testing.T) {
		model := tttcModelBase("custom-model", "Custom model", string(ai.KnownApiOpenaiCompletions), "custom-provider", nil)
		model.Reasoning = false
		payload := tttcCapturePayload(t, model, sharedCtx)
		if names := tttcFieldToolNames(t, tttcGetArrField(t, payload, "tools"), true); !slices.Equal(names, []string{"late_tool"}) {
			t.Fatalf("tools = %v", names)
		}
		msgs := tttcGetArrField(t, payload, "messages")
		if len(msgs) != 2 {
			t.Fatalf("messages len = %d, want 2", len(msgs))
		}
		if r0, _ := tttcObj(t, msgs[0]).Get("role"); r0 != "system" {
			t.Fatalf("msgs[0].role = %v", r0)
		}
		if r1, _ := tttcObj(t, msgs[1]).Get("role"); r1 != "user" {
			t.Fatalf("msgs[1].role = %v", r1)
		}
		if c0, _ := tttcObj(t, msgs[0]).Get("content"); c0 != "base prompt\n\nupdated guidance\n\n<rules>\nnew rules\n</rules>" {
			t.Fatalf("msgs[0].content = %q", c0)
		}
	})
}
