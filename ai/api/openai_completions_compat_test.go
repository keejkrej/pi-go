package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

type toccRoundTripperFunc func(*http.Request) (*http.Response, error)

func (f toccRoundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func toccBoolPtr(b bool) *bool                                  { return &b }
func toccStrPtr(s string) *string                               { return &s }
func toccFloatPtr(f float64) *float64                           { return &f }
func toccIntPtr(i int) *int                                     { return &i }
func toccThinkingLevelPtr(l ai.ThinkingLevel) *ai.ThinkingLevel { return &l }

func toccDoneSSE() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(
			"data: {\"id\":\"chatcmpl_1\",\"model\":\"gpt-4o-mini\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2,\"total_tokens\":12}}\n\n" +
				"data: [DONE]\n\n",
		)),
	}
}

func toccCaptureClient(outBody *map[string]any, outHeaders *http.Header) *http.Client {
	return &http.Client{
		Transport: toccRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if outHeaders != nil {
				*outHeaders = req.Header.Clone()
			}
			raw, _ := io.ReadAll(req.Body)
			if outBody != nil {
				*outBody = nil
				_ = json.Unmarshal(raw, outBody)
			}
			return toccDoneSSE(), nil
		}),
	}
}

func toccBaseModel(provider ai.ProviderId, baseURL string, modelID string) *ai.Model {
	return &ai.Model{
		Id:            modelID,
		Name:          modelID,
		Api:           string(ai.KnownApiOpenaiCompletions),
		Provider:      provider,
		BaseUrl:       baseURL,
		Reasoning:     true,
		Input:         []ai.ModelModality{ai.ModelModalityText},
		ContextWindow: 128000,
		MaxTokens:     16384,
	}
}

func toccUserCtx(prompt string, tools ...*ai.Tool) *ai.TranscriptContext {
	return ai.NormalizeContext(&ai.Context{
		SystemPrompt: toccStrPtr("System prompt"),
		Messages: ai.MessageList{
			&ai.UserMessage{
				Role:      "user",
				Content:   ai.UserMessageContent{Text: &prompt},
				Timestamp: 1,
			},
		},
		Tools: tools,
	})
}

func toccSimpleTool() *ai.Tool {
	props := omap.NewMap[string, *typebox.Schema]()
	props.Set("required", typebox.String())
	props.Set("optional", typebox.Optional(typebox.String()))
	return &ai.Tool{
		Name:        "ping",
		Description: "Ping tool",
		Parameters:  typebox.Object(props),
	}
}

func TestOpenAICompletionsCacheControlFormat(t *testing.T) {
	t.Run("adds cache_control with compat.CacheControlFormat=anthropic and on openrouter anthropic/* auto-detect", func(t *testing.T) {
		ccf := ai.CacheControlFormatAnthropic
		compatModel := toccBaseModel("custom-proxy", "https://proxy.example.com/v1", "claude-sonnet")
		compatModel.Compat.V = &ai.OpenAICompletionsCompat{CacheControlFormat: &ccf}

		var body map[string]any
		res, err := StreamOpenaiCompletions(context.Background(), compatModel, toccUserCtx("hello"), &OpenAICompletionsOptions{
			StreamOptions: ai.StreamOptions{ApiKey: toccStrPtr("k"), HTTPClient: toccCaptureClient(&body, nil)},
		}).Result(context.Background())
		if err != nil || res.StopReason != ai.StopReasonStop {
			t.Fatalf("unexpected error/stop: %v / %s", err, res.StopReason)
		}
		rawMsgs, _ := json.Marshal(body["messages"])
		if !strings.Contains(string(rawMsgs), `"cache_control":{"type":"ephemeral"}`) {
			t.Fatalf("expected cache_control on compat model messages: %s", string(rawMsgs))
		}

		// Auto-detected openrouter + anthropic/*
		orModel := toccBaseModel("openrouter", "https://openrouter.ai/api/v1", "anthropic/claude-sonnet-4-5")
		var orBody map[string]any
		res, err = StreamOpenaiCompletions(context.Background(), orModel, toccUserCtx("hello"), &OpenAICompletionsOptions{
			StreamOptions: ai.StreamOptions{ApiKey: toccStrPtr("k"), HTTPClient: toccCaptureClient(&orBody, nil)},
		}).Result(context.Background())
		if err != nil || res.StopReason != ai.StopReasonStop {
			t.Fatalf("unexpected error/stop: %v / %s", err, res.StopReason)
		}
		rawOrMsgs, _ := json.Marshal(orBody["messages"])
		if !strings.Contains(string(rawOrMsgs), `"cache_control":{"type":"ephemeral"}`) {
			t.Fatalf("expected cache_control on openrouter anthropic model: %s", string(rawOrMsgs))
		}
	})

	t.Run("moves marker onto trailing tool result and omits when cacheRetention=none", func(t *testing.T) {
		ccf := ai.CacheControlFormatAnthropic
		model := toccBaseModel("custom-proxy", "https://proxy.example.com/v1", "claude-sonnet")
		model.Compat.V = &ai.OpenAICompletionsCompat{CacheControlFormat: &ccf}

		args := jsonx.NewObject()
		args.Set("required", "v")
		ctx := ai.NormalizeContext(&ai.Context{
			Messages: ai.MessageList{
				&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: toccStrPtr("first")}, Timestamp: 1},
				&ai.AssistantMessage{
					Role:       "assistant",
					Api:        ai.KnownApiOpenaiCompletions,
					Provider:   "custom-proxy",
					Model:      "claude-sonnet",
					Content:    ai.ContentList{&ai.ToolCall{Type: "toolCall", Id: "call_1", Name: "ping", Arguments: args}},
					StopReason: ai.StopReasonToolUse,
					Timestamp:  2,
				},
				&ai.ToolResultMessage{
					Role:       "toolResult",
					ToolCallId: "call_1",
					ToolName:   "ping",
					Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: "pong"}},
					Timestamp:  3,
				},
			},
		})

		var body map[string]any
		_, err := StreamOpenaiCompletions(context.Background(), model, ctx, &OpenAICompletionsOptions{
			StreamOptions: ai.StreamOptions{ApiKey: toccStrPtr("k"), HTTPClient: toccCaptureClient(&body, nil)},
		}).Result(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		msgs := body["messages"].([]any)
		lastMsgRaw, _ := json.Marshal(msgs[len(msgs)-1])
		firstMsgRaw, _ := json.Marshal(msgs[0])
		if !strings.Contains(string(lastMsgRaw), `"cache_control":{"type":"ephemeral"}`) {
			t.Fatalf("expected trailing tool message to carry cache_control, got %s", string(lastMsgRaw))
		}
		if strings.Contains(string(firstMsgRaw), `"cache_control"`) {
			t.Fatalf("expected user message not to carry cache_control when trailing is toolResult, got %s", string(firstMsgRaw))
		}

		// Omitted when CacheRetention == None
		none := ai.CacheRetentionNone
		var noneBody map[string]any
		_, err = StreamOpenaiCompletions(context.Background(), model, ctx, &OpenAICompletionsOptions{
			StreamOptions: ai.StreamOptions{ApiKey: toccStrPtr("k"), CacheRetention: &none, HTTPClient: toccCaptureClient(&noneBody, nil)},
		}).Result(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		rawNone, _ := json.Marshal(noneBody["messages"])
		if strings.Contains(string(rawNone), `"cache_control"`) {
			t.Fatalf("expected no cache_control when cacheRetention=none: %s", string(rawNone))
		}
	})
}

func TestOpenAICompletionsEmptyToolsAndMaxTokens(t *testing.T) {
	t.Run("omits tools when empty unless RequiresToolsWithToolHistory with tool history, and applies max_completion_tokens clamping", func(t *testing.T) {
		model := toccBaseModel("openai", "https://api.openai.com/v1", "gpt-5.2")
		model.ContextWindow = 8192
		model.MaxTokens = 4096

		var body map[string]any
		_, err := StreamSimpleOpenaiCompletions(context.Background(), model, toccUserCtx("hi"), &ai.SimpleStreamOptions{
			StreamOptions: ai.StreamOptions{ApiKey: toccStrPtr("k"), HTTPClient: toccCaptureClient(&body, nil)},
		}).Result(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, hasTools := body["tools"]; hasTools {
			t.Fatalf("expected tools omitted when empty, got %v", body["tools"])
		}
		if body["max_completion_tokens"] == nil {
			t.Fatalf("expected max_completion_tokens to be set")
		}

		// With RequiresToolsWithToolHistory and tool history present
		reqToolsModel := toccBaseModel("proxy", "https://proxy.example.com/v1", "model-x")
		reqToolsModel.Compat.V = &ai.OpenAICompletionsCompat{RequiresToolsWithToolHistory: toccBoolPtr(true)}
		args := jsonx.NewObject()
		histCtx := ai.NormalizeContext(&ai.Context{
			Messages: ai.MessageList{
				&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: toccStrPtr("run")}, Timestamp: 1},
				&ai.AssistantMessage{
					Role: "assistant", Api: ai.KnownApiOpenaiCompletions, Provider: "proxy", Model: "model-x",
					Content:    ai.ContentList{&ai.ToolCall{Type: "toolCall", Id: "c1", Name: "ping", Arguments: args}},
					StopReason: ai.StopReasonToolUse, Timestamp: 2,
				},
				&ai.ToolResultMessage{
					Role: "toolResult", ToolCallId: "c1", ToolName: "ping",
					Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "ok"}}, Timestamp: 3,
				},
			},
		})
		var histBody map[string]any
		_, err = StreamSimpleOpenaiCompletions(context.Background(), reqToolsModel, histCtx, &ai.SimpleStreamOptions{
			StreamOptions: ai.StreamOptions{ApiKey: toccStrPtr("k"), HTTPClient: toccCaptureClient(&histBody, nil)},
		}).Result(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		toolsSlice, ok := histBody["tools"].([]any)
		if !ok || len(toolsSlice) != 0 {
			t.Fatalf("expected empty tools array [], got %#v", histBody["tools"])
		}
	})
}

func TestOpenAICompletionsPromptCacheAndSessionAffinity(t *testing.T) {
	t.Run("sets prompt_cache_key clamped to 64 runes, prompt_cache_retention=24h, and session affinity headers when enabled", func(t *testing.T) {
		model := toccBaseModel("openai", "https://api.openai.com/v1", "gpt-4o-mini")
		model.Compat.V = &ai.OpenAICompletionsCompat{
			SendSessionAffinityHeaders: toccBoolPtr(true),
		}
		longSess := strings.Repeat("🔥", 80)
		var body map[string]any
		var headers http.Header
		_, err := StreamOpenaiCompletions(context.Background(), model, toccUserCtx("hi"), &OpenAICompletionsOptions{
			StreamOptions: ai.StreamOptions{
				ApiKey:     toccStrPtr("k"),
				SessionId:  &longSess,
				HTTPClient: toccCaptureClient(&body, &headers),
				Env:        ai.ProviderEnv{"PI_CACHE_RETENTION": "long"},
			},
		}).Result(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		pck, _ := body["prompt_cache_key"].(string)
		if len([]rune(pck)) != 64 {
			t.Fatalf("expected 64-rune prompt_cache_key, got %d (%q)", len([]rune(pck)), pck)
		}
		if body["prompt_cache_retention"] != "24h" {
			t.Fatalf("expected prompt_cache_retention=24h, got %v", body["prompt_cache_retention"])
		}
		if headers.Get("session_id") != longSess || headers.Get("x-client-request-id") != longSess || headers.Get("x-session-affinity") != longSess {
			t.Fatalf("expected OpenAI session affinity headers, got %+v", headers)
		}
	})

	t.Run("respects promptCacheKeyMode, cacheRetention=none, openai-nosession, openrouter x-session-id, and header overrides", func(t *testing.T) {
		sess := "sess-abc"

		// Proxy with PromptCacheKeyMode=enabled + OpenAI nosession + custom header override
		proxyModel := toccBaseModel("proxy", "https://proxy.example.com/v1", "m1")
		modeEnabled := ai.PromptCacheKeyModeEnabled
		nosess := ai.SessionAffinityFormatOpenaiNosession
		proxyModel.Compat.V = &ai.OpenAICompletionsCompat{
			PromptCacheKeyMode:         &modeEnabled,
			SendSessionAffinityHeaders: toccBoolPtr(true),
			SessionAffinityFormat:      &nosess,
		}
		customHdrs := omap.NewMap[string, *string]()
		customHdrs.Set("x-session-affinity", toccStrPtr("override-affinity"))
		var body map[string]any
		var headers http.Header
		_, err := StreamOpenaiCompletions(context.Background(), proxyModel, toccUserCtx("hi"), &OpenAICompletionsOptions{
			StreamOptions: ai.StreamOptions{
				ApiKey:     toccStrPtr("k"),
				SessionId:  &sess,
				Headers:    customHdrs,
				HTTPClient: toccCaptureClient(&body, &headers),
			},
		}).Result(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if body["prompt_cache_key"] != sess {
			t.Fatalf("expected prompt_cache_key=%s on enabled proxy, got %v", sess, body["prompt_cache_key"])
		}
		if headers.Get("x-client-request-id") != sess || headers.Get("session_id") != "" || headers.Get("x-session-affinity") != "override-affinity" {
			t.Fatalf("unexpected openai-nosession + override headers: %+v", headers)
		}

		// Disabled on direct OpenAI + OpenRouter auto x-session-id
		orModel := toccBaseModel("openrouter", "https://openrouter.ai/api/v1", "openai/gpt-4o-mini")
		var orBody map[string]any
		var orHeaders http.Header
		_, err = StreamOpenaiCompletions(context.Background(), orModel, toccUserCtx("hi"), &OpenAICompletionsOptions{
			StreamOptions: ai.StreamOptions{
				ApiKey:     toccStrPtr("k"),
				SessionId:  &sess,
				HTTPClient: toccCaptureClient(&orBody, &orHeaders),
			},
		}).Result(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if orHeaders.Get("x-session-id") != sess {
			t.Fatalf("expected OpenRouter x-session-id=%s, got %q", sess, orHeaders.Get("x-session-id"))
		}

		// cacheRetention=none omits both prompt_cache_key and session affinity headers
		directOpenAI := toccBaseModel("openai", "https://api.openai.com/v1", "gpt-4o-mini")
		none := ai.CacheRetentionNone
		var noneBody map[string]any
		var noneHeaders http.Header
		_, err = StreamOpenaiCompletions(context.Background(), directOpenAI, toccUserCtx("hi"), &OpenAICompletionsOptions{
			StreamOptions: ai.StreamOptions{
				ApiKey:         toccStrPtr("k"),
				SessionId:      &sess,
				CacheRetention: &none,
				HTTPClient:     toccCaptureClient(&noneBody, &noneHeaders),
			},
		}).Result(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := noneBody["prompt_cache_key"]; ok {
			t.Fatalf("expected no prompt_cache_key on cacheRetention=none, got %v", noneBody["prompt_cache_key"])
		}
		if noneHeaders.Get("session_id") != "" || noneHeaders.Get("x-client-request-id") != "" {
			t.Fatalf("expected session affinity headers omitted on cacheRetention=none, got %+v", noneHeaders)
		}
	})
}

func TestOpenAICompletionsThinkingBudgetsAndFormats(t *testing.T) {
	t.Run("supports thinking_token_budget, field overrides, xhigh clamping, headroom, and chat_template_kwargs $var", func(t *testing.T) {
		model := toccBaseModel("custom", "https://custom.example.com/v1", "reasoner-1")
		model.MaxTokens = 16384
		model.ContextWindow = 200000
		model.Compat.V = &ai.OpenAICompletionsCompat{
			SupportsThinkingTokenBudget: toccBoolPtr(true),
		}

		// 1. Emits thinking_token_budget for medium
		var body map[string]any
		med := ai.ThinkingLevelMedium
		_, err := StreamSimpleOpenaiCompletions(context.Background(), model, toccUserCtx("hi"), &ai.SimpleStreamOptions{
			StreamOptions: ai.StreamOptions{ApiKey: toccStrPtr("k"), HTTPClient: toccCaptureClient(&body, nil)},
			Reasoning:     &med,
		}).Result(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got, _ := body["thinking_token_budget"].(float64); got != 8192 {
			t.Fatalf("expected thinking_token_budget=8192, got %v", body["thinking_token_budget"])
		}

		// 2. Clamps xhigh to high budget and leaves 1024 headroom when maxTokens ceiling is tight
		xhigh := ai.ThinkingLevelXhigh
		tightCallerMax := 4096
		var tightBody map[string]any
		_, err = StreamSimpleOpenaiCompletions(context.Background(), model, toccUserCtx("hi"), &ai.SimpleStreamOptions{
			StreamOptions: ai.StreamOptions{ApiKey: toccStrPtr("k"), MaxTokens: &tightCallerMax, HTTPClient: toccCaptureClient(&tightBody, nil)},
			Reasoning:     &xhigh,
			ThinkingBudgets: &ai.ThinkingBudgets{
				High: toccIntPtr(20000),
			},
		}).Result(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if budget, _ := tightBody["thinking_token_budget"].(float64); budget != float64(tightCallerMax-1024) {
			t.Fatalf("expected budget clamped to tightCallerMax-1024=3072, got %v", tightBody["thinking_token_budget"])
		}

		var modelCapBody map[string]any
		_, err = StreamSimpleOpenaiCompletions(context.Background(), model, toccUserCtx("hi"), &ai.SimpleStreamOptions{
			StreamOptions: ai.StreamOptions{ApiKey: toccStrPtr("k"), HTTPClient: toccCaptureClient(&modelCapBody, nil)},
			Reasoning:     &xhigh,
			ThinkingBudgets: &ai.ThinkingBudgets{
				High: toccIntPtr(20000),
			},
		}).Result(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if budget, _ := modelCapBody["thinking_token_budget"].(float64); budget != float64(16384-1024) {
			t.Fatalf("expected budget clamped to modelMaxTokens-1024=15360, got %v", modelCapBody["thinking_token_budget"])
		}

		// 3. ThinkingTokenBudgetField=thinking_budget and ChatTemplateKwargs $var=thinking.budget with OmitWhenOff
		fieldBudget := ai.ThinkingTokenBudgetFieldThinkingBudget
		omitWhenOff := true
		kwargs := omap.NewMap[string, ai.ChatTemplateKwargValue]()
		kwargs.Set("thinking_budget", &ai.ChatTemplateKwargVar{Var: ai.ChatTemplateKwargVarNameThinkingBudget, OmitWhenOff: &omitWhenOff})
		kwargs.Set("thinking_flag", &ai.ChatTemplateKwargVar{Var: ai.ChatTemplateKwargVarNameThinkingEnabled})
		m2 := toccBaseModel("custom", "https://custom.example.com/v1", "r2")
		m2.MaxTokens = 32768
		tfCTVar := ai.ThinkingFormatChatTemplate
		m2.Compat.V = &ai.OpenAICompletionsCompat{
			SupportsThinkingTokenBudget: toccBoolPtr(true),
			ThinkingTokenBudgetField:    &fieldBudget,
			ThinkingFormat:              &tfCTVar,
			ChatTemplateKwargs:          kwargs,
		}
		low := ai.ThinkingLevelLow
		var lowBody map[string]any
		_, err = StreamSimpleOpenaiCompletions(context.Background(), m2, toccUserCtx("hi"), &ai.SimpleStreamOptions{
			StreamOptions: ai.StreamOptions{ApiKey: toccStrPtr("k"), HTTPClient: toccCaptureClient(&lowBody, nil)},
			Reasoning:     &low,
		}).Result(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got, _ := lowBody["thinking_budget"].(float64); got != 2048 {
			t.Fatalf("expected thinking_budget=2048, got %v", lowBody["thinking_budget"])
		}
		if _, hasDefault := lowBody["thinking_token_budget"]; hasDefault {
			t.Fatalf("expected default thinking_token_budget omitted when field overridden")
		}
		ctKwargs, _ := lowBody["chat_template_kwargs"].(map[string]any)
		if ctKwargs == nil || ctKwargs["thinking_budget"] != float64(2048) || ctKwargs["thinking_flag"] != true {
			t.Fatalf("unexpected chat_template_kwargs for low: %#v", lowBody["chat_template_kwargs"])
		}

		// Off reasoning omits OmitWhenOff var and sets thinking_flag=false
		var offBody map[string]any
		_, err = StreamSimpleOpenaiCompletions(context.Background(), m2, toccUserCtx("hi"), &ai.SimpleStreamOptions{
			StreamOptions: ai.StreamOptions{ApiKey: toccStrPtr("k"), HTTPClient: toccCaptureClient(&offBody, nil)},
		}).Result(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, hasBudget := offBody["thinking_budget"]; hasBudget {
			t.Fatalf("expected thinking_budget omitted when reasoning off")
		}
		offKwargs, _ := offBody["chat_template_kwargs"].(map[string]any)
		if _, hasKBudget := offKwargs["thinking_budget"]; hasKBudget || offKwargs["thinking_flag"] != false {
			t.Fatalf("unexpected off chat_template_kwargs: %#v", offKwargs)
		}
	})

	t.Run("supports vllmPriority, toolChoice, strict mode, ThinkingLevelMap, zaiToolStream, and thinking formats", func(t *testing.T) {
		tool := toccSimpleTool()
		tool.ConstrainedSampling = ai.ToolConstrainedSamplingValue{
			V: &ai.ConstrainedSamplingJsonSchema{Type: "json_schema", Strict: ai.ConstrainedSamplingStrictPrefer},
		}

		// Strict enabled + ThinkingLevelMap + zaiToolStream + vllmPriority
		m := toccBaseModel("custom", "https://custom.example.com/v1", "m-full")
		m.ThinkingLevelMap = &ai.ThinkingLevelMap{Medium: jsonx.Some("default")}
		m.Compat.V = &ai.OpenAICompletionsCompat{
			SupportsStrictMode: toccBoolPtr(true),
			ZaiToolStream:      toccBoolPtr(true),
			VllmPriority:       toccFloatPtr(2),
		}
		med := ai.ThinkingLevelMedium
		var body map[string]any
		_, err := StreamSimpleOpenaiCompletions(context.Background(), m, toccUserCtx("call ping", tool), &ai.SimpleStreamOptions{
			StreamOptions: ai.StreamOptions{ApiKey: toccStrPtr("k"), HTTPClient: toccCaptureClient(&body, nil)},
			Reasoning:     &med,
		}).Result(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if body["priority"] != float64(2) || body["tool_stream"] != true || body["reasoning_effort"] != "default" {
			t.Fatalf("expected priority=2, tool_stream=true, reasoning_effort=default, got %+v", body)
		}
		fn := body["tools"].([]any)[0].(map[string]any)["function"].(map[string]any)
		if fn["strict"] != true {
			t.Fatalf("expected strict=true, got %v", fn["strict"])
		}

		// Unknown endpoint defaults strict off + ToolChoice=none omits tools
		localModel := toccBaseModel("custom", "http://localhost:11434/v1", "local-m")
		var localBody map[string]any
		tcNone := ai.ToolChoiceNone
		_, err = StreamSimpleOpenaiCompletions(context.Background(), localModel, toccUserCtx("no tools"), &ai.SimpleStreamOptions{
			StreamOptions: ai.StreamOptions{ApiKey: toccStrPtr("k"), HTTPClient: toccCaptureClient(&localBody, nil)},
			ToolChoice:    &tcNone,
		}).Result(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if localBody["tool_choice"] != "none" {
			t.Fatalf("expected tool_choice=none, got %v", localBody["tool_choice"])
		}
		if _, hasTools := localBody["tools"]; hasTools {
			t.Fatalf("expected no tools field, got %v", localBody["tools"])
		}

		// OpenRouter reasoning={effort} & chat-template / qwen-chat-template / ant-ling
		orModel := toccBaseModel("openrouter", "https://openrouter.ai/api/v1", "openai/gpt-5")
		tfOr := ai.ThinkingFormatOpenrouter
		orModel.Compat.V = &ai.OpenAICompletionsCompat{ThinkingFormat: &tfOr}
		var orBody map[string]any
		_, _ = StreamSimpleOpenaiCompletions(context.Background(), orModel, toccUserCtx("hi"), &ai.SimpleStreamOptions{
			StreamOptions: ai.StreamOptions{ApiKey: toccStrPtr("k"), HTTPClient: toccCaptureClient(&orBody, nil)},
			Reasoning:     &med,
		}).Result(context.Background())
		if rMap, _ := orBody["reasoning"].(map[string]any); rMap["effort"] != "medium" {
			t.Fatalf("expected reasoning.effort=medium, got %#v", orBody["reasoning"])
		}

		ctModel := toccBaseModel("custom", "https://custom.example.com/v1", "ct")
		tfCT := ai.ThinkingFormatQwenChatTemplate
		ctModel.Compat.V = &ai.OpenAICompletionsCompat{ThinkingFormat: &tfCT}
		var ctBody map[string]any
		_, _ = StreamSimpleOpenaiCompletions(context.Background(), ctModel, toccUserCtx("hi"), &ai.SimpleStreamOptions{
			StreamOptions: ai.StreamOptions{ApiKey: toccStrPtr("k"), HTTPClient: toccCaptureClient(&ctBody, nil)},
			Reasoning:     &med,
		}).Result(context.Background())
		if kw, _ := ctBody["chat_template_kwargs"].(map[string]any); kw["enable_thinking"] != true || kw["preserve_thinking"] != true {
			t.Fatalf("expected qwen-chat-template kwargs enable_thinking+preserve_thinking, got %#v", ctBody["chat_template_kwargs"])
		}

		lingModel := toccBaseModel("custom", "https://custom.example.com/v1", "ling")
		lingModel.ThinkingLevelMap = &ai.ThinkingLevelMap{Medium: jsonx.Some("high")}
		tfLing := ai.ThinkingFormatAntLing
		lingModel.Compat.V = &ai.OpenAICompletionsCompat{ThinkingFormat: &tfLing}
		var lingBody map[string]any
		_, _ = StreamSimpleOpenaiCompletions(context.Background(), lingModel, toccUserCtx("hi"), &ai.SimpleStreamOptions{
			StreamOptions: ai.StreamOptions{ApiKey: toccStrPtr("k"), HTTPClient: toccCaptureClient(&lingBody, nil)},
			Reasoning:     &med,
		}).Result(context.Background())
		if rMap, _ := lingBody["reasoning"].(map[string]any); rMap == nil || rMap["effort"] != "high" {
			t.Fatalf("expected ant-ling reasoning.effort=high, got %#v", lingBody["reasoning"])
		}
	})
}
