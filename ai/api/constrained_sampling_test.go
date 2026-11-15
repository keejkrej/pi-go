// Ported from packages/ai/test/constrained-sampling.test.ts (pi v1.5.2).

package api

import (
	"iter"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

func tcsModel() *ai.Model {
	return &ai.Model{
		Id:            "gpt-test",
		Name:          "GPT Test",
		Api:           string(ai.KnownApiOpenaiResponses),
		Provider:      "openai",
		BaseUrl:       "https://api.openai.com/v1",
		Reasoning:     false,
		Input:         []ai.ModelModality{ai.ModelModalityText, ai.ModelModalityImage},
		ContextWindow: 128000,
		MaxTokens:     4096,
	}
}

func tcsOutput() *ai.AssistantMessage {
	return &ai.AssistantMessage{
		Role:       "assistant",
		Content:    ai.ContentList{},
		Api:        ai.KnownApiOpenaiResponses,
		Provider:   "openai",
		Model:      "gpt-test",
		StopReason: ai.StopReasonPending,
		Timestamp:  1,
	}
}

func tcsIterateEvents(events []*jsonx.Object) iter.Seq2[*jsonx.Object, error] {
	return func(yield func(*jsonx.Object, error) bool) {
		for _, ev := range events {
			if !yield(ev, nil) {
				return
			}
		}
	}
}

func tcsMustObj(t *testing.T, raw string) *jsonx.Object {
	t.Helper()
	parsed, err := jsonx.Parse([]byte(raw))
	if err != nil {
		t.Fatalf("jsonx.Parse(%s): %v", raw, err)
	}
	obj, ok := parsed.(*jsonx.Object)
	if !ok {
		t.Fatalf("expected object, got %T", parsed)
	}
	return obj
}

func tcsDefaultParams() *typebox.Schema {
	props := omap.NewMap[string, *typebox.Schema]()
	props.Set("payload", typebox.String())
	opts := jsonx.NewObject()
	opts.Set("additionalProperties", false)
	return typebox.Object(props, opts)
}

func tcsMakeTool(cs ...ai.ToolConstrainedSampling) *ai.Tool {
	tool := &ai.Tool{
		Name:        "sample_tool",
		Description: "Sample tool",
		Parameters:  tcsDefaultParams(),
	}
	if len(cs) > 0 {
		tool.ConstrainedSampling = ai.ToolConstrainedSamplingValue{V: cs[0]}
	}
	return tool
}

func tcsBoolPtr(b bool) *bool    { return &b }
func tcsStrPtr(s string) *string { return &s }

func TestConstrainedToolSampling(t *testing.T) {
	t.Run("converts supported constraints and falls back when unsupported", func(t *testing.T) {
		prefer := &ai.ConstrainedSamplingJsonSchema{Type: "json_schema", Strict: ai.ConstrainedSamplingStrictPrefer}
		tools, err := ConvertResponsesTools([]*ai.Tool{tcsMakeTool(prefer)}, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		firstObj, ok := tools[0].(*jsonx.Object)
		if !ok {
			t.Fatalf("expected *jsonx.Object, got %T", tools[0])
		}
		if typ, _ := firstObj.GetString("type"); typ != "function" {
			t.Fatalf("expected type=function, got %q", typ)
		}
		if strict, _ := firstObj.Get("strict"); strict != true {
			t.Fatalf("expected strict=true, got %v", strict)
		}

		reqTool := tcsMakeTool(&ai.ConstrainedSamplingJsonSchema{Type: "json_schema", Strict: ai.ConstrainedSamplingStrictRequire})
		if _, err := ConvertResponsesTools([]*ai.Tool{reqTool}, &ConvertResponsesToolsOptions{SupportsStrictMode: tcsBoolPtr(false)}); err == nil ||
			!strings.Contains(err.Error(), `Tool "sample_tool" requires JSON-schema constrained sampling`) {
			t.Fatalf("unexpected error for unsupported require: %v", err)
		}

		lark := "start: /[a-z]+/"
		grammarTool := tcsMakeTool(&ai.ConstrainedSamplingGrammar{
			Type:     "grammar",
			Variants: ai.GrammarVariants{OpenaiLark: &lark},
		})
		gTools, err := ConvertResponsesTools([]*ai.Tool{grammarTool}, &ConvertResponsesToolsOptions{SupportsOpenAIGrammarTools: true})
		if err != nil {
			t.Fatalf("grammar conversion failed: %v", err)
		}
		gRaw, _ := jsonx.Stringify(gTools[0])
		if !strings.Contains(gRaw, `"type":"custom"`) || !strings.Contains(gRaw, `"syntax":"lark"`) || !strings.Contains(gRaw, `"definition":"start: /[a-z]+/"`) {
			t.Fatalf("unexpected custom grammar tool JSON: %s", gRaw)
		}

		emptyGrammarTool := tcsMakeTool(&ai.ConstrainedSamplingGrammar{Type: "grammar", Variants: ai.GrammarVariants{}})
		if _, err := ConvertResponsesTools([]*ai.Tool{emptyGrammarTool}, &ConvertResponsesToolsOptions{SupportsOpenAIGrammarTools: true}); err == nil ||
			!strings.Contains(err.Error(), `Tool "sample_tool" cannot use grammar constrained sampling: no supported grammar variant was provided`) {
			t.Fatalf("unexpected error for empty variants: %v", err)
		}

		fallbackTools, err := ConvertResponsesTools([]*ai.Tool{grammarTool}, &ConvertResponsesToolsOptions{
			SupportsOpenAIGrammarTools: false,
			SupportsStrictMode:         tcsBoolPtr(false),
		})
		if err != nil {
			t.Fatalf("grammar fallback failed: %v", err)
		}
		fbObj := fallbackTools[0].(*jsonx.Object)
		if typ, _ := fbObj.GetString("type"); typ != "function" {
			t.Fatalf("expected fallback function, got %q", typ)
		}
		if _, exists := fbObj.Get("strict"); exists {
			t.Fatalf("expected no strict key on fallback")
		}

		falseTools, err := ConvertResponsesTools([]*ai.Tool{tcsMakeTool(ai.ToolConstrainedSamplingFalse{})}, nil)
		if err != nil {
			t.Fatalf("false constraint failed: %v", err)
		}
		plainTools, err := ConvertResponsesTools([]*ai.Tool{tcsMakeTool()}, nil)
		if err != nil {
			t.Fatalf("plain tool failed: %v", err)
		}
		sFalse, _ := jsonx.Stringify(falseTools)
		sPlain, _ := jsonx.Stringify(plainTools)
		if sFalse != sPlain {
			t.Fatalf("expected constrainedSampling:false to match plain tool:\ngot:  %s\nwant: %s", sFalse, sPlain)
		}
	})

	t.Run("derives strict provider schemas without changing tool definitions", func(t *testing.T) {
		metaProps := omap.NewMap[string, *typebox.Schema]()
		metaProps.Set("enabled", typebox.Optional(typebox.Boolean()))

		props := omap.NewMap[string, *typebox.Schema]()
		props.Set("path", typebox.String())
		props.Set("offset", typebox.Optional(typebox.Number()))
		props.Set("metadata", typebox.Object(metaProps))
		props.Set("nullable", typebox.Optional(typebox.Union([]*typebox.Schema{typebox.String(), typebox.Null()})))
		parameters := typebox.Object(props)

		origBefore, _ := jsonx.Stringify(parameters)
		strict, err := MakeStrictJsonSchema(parameters, nil)
		if err != nil {
			t.Fatalf("MakeStrictJsonSchema failed: %v", err)
		}
		origAfter, _ := jsonx.Stringify(parameters)
		if origBefore != origAfter {
			t.Fatalf("input schema mutated:\nbefore: %s\nafter:  %s", origBefore, origAfter)
		}
		if _, hasAddl := parameters.Object.Get("additionalProperties"); hasAddl {
			t.Fatalf("expected parameters not to gain additionalProperties")
		}

		strictJSON, _ := jsonx.Stringify(strict)
		if !strings.Contains(strictJSON, `"additionalProperties":false`) ||
			!strings.Contains(strictJSON, `"required":["path","offset","metadata","nullable"]`) ||
			!strings.Contains(strictJSON, `"offset":{"anyOf":[{"type":"number"},{"type":"null"}]}`) ||
			!strings.Contains(strictJSON, `"enabled":{"anyOf":[{"type":"boolean"},{"type":"null"}]}`) ||
			!strings.Contains(strictJSON, `"nullable":{"anyOf":[{"type":"string"},{"type":"null"}]}`) {
			t.Fatalf("unexpected strict schema output: %s", strictJSON)
		}
	})

	t.Run("keeps strict mode for enum-only schemas", func(t *testing.T) {
		props := omap.NewMap[string, *typebox.Schema]()
		props.Set("level", typebox.Enum([]any{"low", "high"}))
		parameters := typebox.Object(props)
		if rawProps, ok := parameters.Object.Get("properties"); !ok || rawProps == nil {
			t.Fatalf("expected properties object")
		} else if _, ok := rawProps.(*jsonx.Object); !ok {
			t.Fatalf("expected *jsonx.Object for properties, got %T", rawProps)
		}
		tool := tcsMakeTool(&ai.ConstrainedSamplingJsonSchema{Type: "json_schema", Strict: ai.ConstrainedSamplingStrictPrefer})
		tool.Parameters = parameters
		converted, err := ConvertResponsesTools([]*ai.Tool{tool}, &ConvertResponsesToolsOptions{SupportsStrictMode: tcsBoolPtr(true)})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if strict, _ := converted[0].(*jsonx.Object).Get("strict"); strict != true {
			t.Fatalf("expected strict=true for enum-only schema, got %v", strict)
		}
	})

	t.Run("falls back or rejects schemas that cannot be safely converted", func(t *testing.T) {
		apProps := omap.NewMap[string, *typebox.Schema]()
		apOpts := jsonx.NewObject()
		apOpts.Set("additionalProperties", typebox.String())
		apMetaProps := omap.NewMap[string, *typebox.Schema]()
		apMetaProps.Set("metadata", typebox.Object(apProps, apOpts))

		unionObjProps := omap.NewMap[string, *typebox.Schema]()
		nestedProps := omap.NewMap[string, *typebox.Schema]()
		nestedProps.Set("nested", typebox.String())
		unionObjProps.Set("value", typebox.Union([]*typebox.Schema{typebox.Object(nestedProps), typebox.Null()}))

		refSchema, _ := typebox.FromJSON(tcsMustObj(t, `{"type":"object","properties":{"child":{"$ref":"https://example.com/child.json"}},"required":["child"]}`))
		constSchema, _ := typebox.FromJSON(tcsMustObj(t, `{"type":"object","properties":{"mode":{"const":"fast"}},"required":["mode"]}`))

		arrUnknownProps := omap.NewMap[string, *typebox.Schema]()
		arrUnknownProps.Set("commandArguments", typebox.Optional(typebox.Array(typebox.Unknown())))

		anyProps := omap.NewMap[string, *typebox.Schema]()
		anyOpts := jsonx.NewObject()
		anyOpts.Set("description", "Any JSON value")
		anyProps.Set("value", typebox.Any(anyOpts))

		cases := []struct {
			schema *typebox.Schema
			errSub string
		}{
			{typebox.Object(apMetaProps), "additionalProperties is unsupported"},
			{typebox.Intersect([]*typebox.Schema{
				func() *typebox.Schema {
					p := omap.NewMap[string, *typebox.Schema]()
					p.Set("a", typebox.String())
					return typebox.Object(p)
				}(),
				func() *typebox.Schema {
					p := omap.NewMap[string, *typebox.Schema]()
					p.Set("b", typebox.Number())
					return typebox.Object(p)
				}(),
			}), "allOf schemas are unsupported"},
			{typebox.Object(unionObjProps), "object and array unions are unsupported"},
			{refSchema, "$ref schemas are unsupported"},
			{typebox.Object(arrUnknownProps), "schemas without a type are unsupported"},
			{typebox.Object(anyProps), "schemas without a type are unsupported"},
			{constSchema, "schemas without a type are unsupported"},
		}

		for _, tc := range cases {
			tool := tcsMakeTool(&ai.ConstrainedSamplingJsonSchema{Type: "json_schema", Strict: ai.ConstrainedSamplingStrictPrefer})
			tool.Parameters = tc.schema

			if _, err := MakeStrictJsonSchema(tc.schema, nil); err == nil || !strings.Contains(err.Error(), tc.errSub) {
				t.Fatalf("MakeStrictJsonSchema got err=%v, want substring %q", err, tc.errSub)
			}
			if res, err := ResolveJsonSchemaStrictSampling(tool, true, nil); err != nil || res != nil {
				t.Fatalf("expected (nil, nil) for prefer fallback, got (%v, %v)", res, err)
			}
			converted, err := ConvertResponsesTools([]*ai.Tool{tool}, &ConvertResponsesToolsOptions{SupportsStrictMode: tcsBoolPtr(true)})
			if err != nil {
				t.Fatalf("ConvertResponsesTools prefer fallback failed: %v", err)
			}
			if strict, _ := converted[0].(*jsonx.Object).Get("strict"); strict != false {
				t.Fatalf("expected strict=false fallback, got %v", strict)
			}

			tool.ConstrainedSampling = ai.ToolConstrainedSamplingValue{
				V: &ai.ConstrainedSamplingJsonSchema{Type: "json_schema", Strict: ai.ConstrainedSamplingStrictRequire},
			}
			if _, err := ResolveJsonSchemaStrictSampling(tool, true, nil); err == nil || !strings.Contains(err.Error(), tc.errSub) {
				t.Fatalf("require mode got err=%v, want substring %q", err, tc.errSub)
			}
		}
	})

	t.Run("replays grammar calls as custom Responses items", func(t *testing.T) {
		for _, invalidRaw := range []string{`{}`, `{"payload":42}`} {
			args := tcsMustObj(t, invalidRaw)
			ctx := ai.NormalizeContext(&ai.Context{
				Messages: ai.MessageList{
					&ai.AssistantMessage{
						Role:       "assistant",
						Api:        ai.KnownApiOpenaiResponses,
						Provider:   "openai",
						Model:      "gpt-test",
						Content:    ai.ContentList{&ai.ToolCall{Type: "toolCall", Id: "call_1|ctc_1", Name: "sample_tool", Arguments: args}},
						StopReason: ai.StopReasonToolUse,
						Timestamp:  1,
					},
					&ai.ToolResultMessage{
						Role:       "toolResult",
						ToolCallId: "call_1|ctc_1",
						ToolName:   "sample_tool",
						Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: "done"}},
						Timestamp:  2,
					},
				},
			})
			_, err := ConvertResponsesMessages(tcsModel(), ctx, map[string]struct{}{"openai": {}}, &ConvertResponsesMessagesOptions{
				GrammarToolInputProperties: map[string]string{"sample_tool": "payload"},
			})
			if err == nil || !strings.Contains(err.Error(), `Grammar tool call "sample_tool" requires argument "payload" to be a string`) {
				t.Fatalf("expected grammar arg validation error, got %v", err)
			}
		}

		ctx := ai.NormalizeContext(&ai.Context{
			Messages: ai.MessageList{
				&ai.AssistantMessage{
					Role:       "assistant",
					Api:        ai.KnownApiOpenaiResponses,
					Provider:   "openai",
					Model:      "gpt-test",
					Content:    ai.ContentList{&ai.ToolCall{Type: "toolCall", Id: "call_1|ctc_1", Name: "sample_tool", Arguments: tcsMustObj(t, `{"payload":"abc"}`)}},
					StopReason: ai.StopReasonToolUse,
					Timestamp:  1,
				},
				&ai.ToolResultMessage{
					Role:       "toolResult",
					ToolCallId: "call_1|ctc_1",
					ToolName:   "sample_tool",
					Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: "done"}},
					Timestamp:  2,
				},
			},
		})
		items, err := ConvertResponsesMessages(tcsModel(), ctx, map[string]struct{}{"openai": {}}, &ConvertResponsesMessagesOptions{
			GrammarToolInputProperties: map[string]string{"sample_tool": "payload"},
		})
		if err != nil {
			t.Fatalf("ConvertResponsesMessages failed: %v", err)
		}
		raw, _ := jsonx.Stringify(items)
		if !strings.Contains(raw, `"type":"custom_tool_call","id":"ctc_1","call_id":"call_1","name":"sample_tool","input":"abc"`) ||
			!strings.Contains(raw, `"type":"custom_tool_call_output","call_id":"call_1","output":"done"`) {
			t.Fatalf("unexpected custom items: %s", raw)
		}
	})

	t.Run("drops foreign item ids when replaying grammar calls as custom Responses items", func(t *testing.T) {
		ctx := ai.NormalizeContext(&ai.Context{
			Messages: ai.MessageList{
				&ai.AssistantMessage{
					Role:       "assistant",
					Api:        ai.KnownApiPiMessages,
					Provider:   "radius",
					Model:      "gpt-other",
					Content:    ai.ContentList{&ai.ToolCall{Type: "toolCall", Id: "call_1|ctc_1", Name: "sample_tool", Arguments: tcsMustObj(t, `{"payload":"abc"}`)}},
					StopReason: ai.StopReasonToolUse,
					Timestamp:  1,
				},
				&ai.ToolResultMessage{
					Role:       "toolResult",
					ToolCallId: "call_1|ctc_1",
					ToolName:   "sample_tool",
					Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: "done"}},
					Timestamp:  2,
				},
			},
		})
		items, err := ConvertResponsesMessages(tcsModel(), ctx, map[string]struct{}{"openai": {}}, &ConvertResponsesMessagesOptions{
			GrammarToolInputProperties: map[string]string{"sample_tool": "payload"},
		})
		if err != nil {
			t.Fatalf("ConvertResponsesMessages failed: %v", err)
		}
		var call *jsonx.Object
		for _, item := range items {
			if obj, ok := item.(*jsonx.Object); ok {
				if typ, _ := obj.GetString("type"); typ == "custom_tool_call" {
					call = obj
					break
				}
			}
		}
		if call == nil {
			t.Fatalf("expected custom_tool_call")
		}
		if _, hasID := call.Get("id"); hasID {
			t.Fatalf("expected foreign id to be dropped")
		}
		if callID, _ := call.GetString("call_id"); callID != "call_1" {
			t.Fatalf("expected call_id=call_1, got %q", callID)
		}
	})

	t.Run("keeps grammar input JSON deltas append-only", func(t *testing.T) {
		buf := &GrammarToolInputJsonBuffer{}
		first, err := AppendGrammarToolInputJsonDelta(buf, "payload", `a"`)
		if err != nil || first == nil {
			t.Fatalf("first append failed: %v", err)
		}
		second, err := CloseGrammarToolInputJson(buf, "payload", tcsStrPtr("a\"\nb"))
		if err != nil || second == nil {
			t.Fatalf("close failed: %v", err)
		}
		parsed, err := jsonx.Parse([]byte(*first + *second))
		if err != nil {
			t.Fatalf("parse joined JSON failed: %v", err)
		}
		if got, _ := parsed.(*jsonx.Object).GetString("payload"); got != "a\"\nb" {
			t.Fatalf("got payload %q, want %q", got, "a\"\nb")
		}
		if again, err := CloseGrammarToolInputJson(buf, "payload", tcsStrPtr("a\"\nb")); err != nil || again != nil {
			t.Fatalf("expected idempotent close (nil, nil), got (%v, %v)", again, err)
		}
		if _, err := CloseGrammarToolInputJson(buf, "payload", tcsStrPtr("changed")); err == nil ||
			!strings.Contains(err.Error(), `grammar tool input for property "payload" changed after it was closed`) {
			t.Fatalf("expected closed mutation error, got %v", err)
		}
		if _, err := AppendGrammarToolInputJsonDelta(buf, "payload", "x"); err == nil ||
			!strings.Contains(err.Error(), `grammar tool input for property "payload" changed after it was closed`) {
			t.Fatalf("expected closed append error, got %v", err)
		}
	})

	t.Run("rejects a final grammar input that does not continue the streamed input", func(t *testing.T) {
		buf := &GrammarToolInputJsonBuffer{}
		if _, err := AppendGrammarToolInputJsonDelta(buf, "payload", "abc"); err != nil {
			t.Fatalf("append failed: %v", err)
		}
		if _, err := CloseGrammarToolInputJson(buf, "payload", tcsStrPtr("abd")); err == nil ||
			!strings.Contains(err.Error(), `grammar tool input for property "payload" changed non-monotonically`) {
			t.Fatalf("expected non-monotonic error, got %v", err)
		}
	})

	t.Run("streams custom tool input in time and memory linear in its length", func(t *testing.T) {
		run := func(size int) uint64 {
			count := size / 32
			deltas := make([]string, count)
			for i := 0; i < count; i++ {
				s := "chunk quoted \\ \n"
				for len(s) < 32 {
					s += "x"
				}
				deltas[i] = s[:32]
			}
			full := strings.Join(deltas, "")
			events := make([]*jsonx.Object, 0, len(deltas)+3)
			events = append(events, tcsMustObj(t, `{"type":"response.output_item.added","output_index":0,"item":{"type":"custom_tool_call","call_id":"call_1","id":"ctc_1","name":"sample_tool","input":""}}`))
			for _, d := range deltas {
				ev := jsonx.NewObject()
				ev.Set("type", "response.custom_tool_call_input.delta")
				ev.Set("output_index", 0)
				ev.Set("item_id", "ctc_1")
				ev.Set("delta", d)
				events = append(events, ev)
			}
			doneInput := jsonx.NewObject()
			doneInput.Set("type", "response.custom_tool_call_input.done")
			doneInput.Set("output_index", 0)
			doneInput.Set("item_id", "ctc_1")
			doneInput.Set("input", full)
			events = append(events, doneInput)

			doneItem := jsonx.NewObject()
			doneItem.Set("type", "response.output_item.done")
			doneItem.Set("output_index", 0)
			itemObj := jsonx.NewObject()
			itemObj.Set("type", "custom_tool_call")
			itemObj.Set("call_id", "call_1")
			itemObj.Set("id", "ctc_1")
			itemObj.Set("name", "sample_tool")
			itemObj.Set("input", full)
			doneItem.Set("item", itemObj)
			events = append(events, doneItem)
			events = append(events, tcsMustObj(t, `{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`))

			output := tcsOutput()
			stream := ai.NewAssistantMessageEventStream()
			var jsonDeltas []string
			doneCh := make(chan struct{})
			go func() {
				defer close(doneCh)
				for ev := range stream.All() {
					if td, ok := ev.(*ai.AssistantMessageEventToolcallDelta); ok {
						jsonDeltas = append(jsonDeltas, td.Delta)
					}
				}
			}()

			var m1, m2 runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&m1)
			err := ProcessResponsesStream(tcsIterateEvents(events), output, stream, tcsModel(), &OpenAIResponsesStreamOptions{
				GrammarToolInputProperties: map[string]string{"sample_tool": "payload"},
			})
			stream.End(output)
			<-doneCh
			runtime.ReadMemStats(&m2)

			if err != nil {
				t.Fatalf("ProcessResponsesStream failed: %v", err)
			}
			joined := strings.Join(jsonDeltas, "")
			parsed, err := jsonx.Parse([]byte(joined))
			if err != nil {
				t.Fatalf("invalid streamed JSON: %v", err)
			}
			if got, _ := parsed.(*jsonx.Object).GetString("payload"); got != full {
				t.Fatalf("streamed payload mismatch (len got=%d want=%d)", len(got), len(full))
			}
			return m2.TotalAlloc - m1.TotalAlloc
		}

		small := run(25_600)
		large := run(256_000)
		if small > 0 && float64(large)/float64(small) >= 25 {
			t.Fatalf("non-linear allocation growth: small=%d large=%d ratio=%.2f", small, large, float64(large)/float64(small))
		}
	})

	t.Run("starts custom Responses tool calls with their initial input", func(t *testing.T) {
		output := tcsOutput()
		stream := ai.NewAssistantMessageEventStream()
		var starts []*jsonx.Object
		var deltas []string
		doneCh := make(chan struct{})
		go func() {
			defer close(doneCh)
			for ev := range stream.All() {
				switch e := ev.(type) {
				case *ai.AssistantMessageEventToolcallStart:
					if tc, ok := e.Partial.Content[e.ContentIndex].(*ai.ToolCall); ok && tc.Arguments != nil {
						starts = append(starts, tc.Arguments.Clone())
					}
				case *ai.AssistantMessageEventToolcallDelta:
					deltas = append(deltas, e.Delta)
				}
			}
		}()

		events := []*jsonx.Object{
			tcsMustObj(t, `{"type":"response.output_item.added","output_index":0,"item":{"type":"custom_tool_call","call_id":"call_1","id":"ctc_1","name":"sample_tool","input":"a"}}`),
			tcsMustObj(t, `{"type":"response.custom_tool_call_input.delta","output_index":0,"item_id":"ctc_1","delta":"b"}`),
			tcsMustObj(t, `{"type":"response.custom_tool_call_input.done","output_index":0,"item_id":"ctc_1","input":"abc"}`),
			tcsMustObj(t, `{"type":"response.output_item.done","output_index":0,"item":{"type":"custom_tool_call","call_id":"call_1","id":"ctc_1","name":"sample_tool","input":"abc"}}`),
			tcsMustObj(t, `{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`),
		}

		err := ProcessResponsesStream(tcsIterateEvents(events), output, stream, tcsModel(), &OpenAIResponsesStreamOptions{
			GrammarToolInputProperties: map[string]string{"sample_tool": "payload"},
		})
		stream.End(output)
		<-doneCh
		if err != nil {
			t.Fatalf("ProcessResponsesStream failed: %v", err)
		}
		if output.StopReason != ai.StopReasonToolUse {
			t.Fatalf("got stopReason=%q, want toolUse", output.StopReason)
		}
		if len(starts) != 1 || !reflect.DeepEqual(starts[0], tcsMustObj(t, `{"payload":"a"}`)) {
			s, _ := jsonx.Stringify(starts)
			t.Fatalf("unexpected starts: %s", s)
		}
		if len(output.Content) != 1 {
			t.Fatalf("expected 1 content block, got %d", len(output.Content))
		}
		tc := output.Content[0].(*ai.ToolCall)
		if tc.Id != "call_1|ctc_1" || tc.Name != "sample_tool" || !reflect.DeepEqual(tc.Arguments, tcsMustObj(t, `{"payload":"abc"}`)) {
			t.Fatalf("unexpected toolCall: %+v", tc)
		}
		parsed, err := jsonx.Parse([]byte(strings.Join(deltas, "")))
		if err != nil {
			t.Fatalf("invalid joined deltas: %v", err)
		}
		if got, _ := parsed.(*jsonx.Object).GetString("payload"); got != "abc" {
			t.Fatalf("got joined delta payload %q, want abc", got)
		}
	})
}
