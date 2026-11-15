package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
	"github.com/klauspost/compress/zstd"
)

func tocrStrPtr(s string) *string { return &s }
func tocrBoolPtr(b bool) *bool    { return &b }
func tocrInt64Ptr(v int64) *int64 { return &v }
func tocrIntPtr(v int) *int       { return &v }

func tocrMockJWT(accountID string) string {
	claims, _ := json.Marshal(map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": accountID,
		},
	})
	return "aaa." + base64.RawURLEncoding.EncodeToString(claims) + ".bbb"
}

func tocrModel(baseURL string) *ai.Model {
	return &ai.Model{
		Id:            "gpt-5.1-codex",
		Name:          "GPT-5.1 Codex",
		Api:           string(ai.KnownApiOpenaiCodexResponses),
		Provider:      "openai-codex",
		BaseUrl:       baseURL,
		Reasoning:     true,
		Input:         []ai.ModelModality{ai.ModelModalityText},
		ContextWindow: 400000,
		MaxTokens:     128000,
	}
}

func tocrUserCtx(text string, tools ...*ai.Tool) *ai.TranscriptContext {
	return ai.NormalizeContext(&ai.Context{
		SystemPrompt: tocrStrPtr("You are a helpful assistant."),
		Messages: ai.MessageList{
			&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: &text}, Timestamp: 1},
		},
		Tools: tools,
	})
}

func tocrCompletedSSEEvents(text string, respID string) string {
	events := []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_1","role":"assistant","status":"in_progress","content":[]}}`,
		`{"type":"response.content_part.added","output_index":0,"content_index":0,"part":{"type":"output_text","text":""}}`,
		`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":` + tocrJSONStr(text) + `}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":` + tocrJSONStr(text) + `}]}}`,
		`{"type":"response.completed","response":{"id":` + tocrJSONStr(respID) + `,"status":"completed","usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8,"input_tokens_details":{"cached_tokens":0}}}}`,
	}
	var sb strings.Builder
	for _, ev := range events {
		sb.WriteString("data: ")
		sb.WriteString(ev)
		sb.WriteString("\n\n")
	}
	return sb.String()
}

func tocrJSONStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func tocrResetPools() {
	CloseOpenAICodexWebSocketSessions(nil)
	ResetOpenAICodexWebSocketDebugStats(nil)
}

func tocrWriteWSJSON(ctx context.Context, conn *websocket.Conn, rawJSON string) error {
	return conn.Write(ctx, websocket.MessageText, []byte(rawJSON))
}

func TestOpenAICodexSSEStreamingAndCompression(t *testing.T) {
	tocrResetPools()
	defer tocrResetPools()

	t.Run("streams SSE responses, validates headers & zstd body compression, forwards raw events, and handles no-blank-line & open-body completion", func(t *testing.T) {
		token := tocrMockJWT("acc_test")
		var mu sync.Mutex
		var sawHeaders http.Header
		var sawDecodedText string
		var sawEncoding string
		var reqCount int
		keepOpenDone := make(chan struct{})

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/backend-api/codex/responses" {
				http.NotFound(w, r)
				return
			}
			rawBody, _ := io.ReadAll(r.Body)
			dec, err := zstd.NewReader(nil)
			if err == nil {
				if decompressed, dErr := dec.DecodeAll(rawBody, nil); dErr == nil {
					var bodyMap map[string]any
					if json.Unmarshal(decompressed, &bodyMap) == nil {
						if inp, ok := bodyMap["input"].([]any); ok && len(inp) > 0 {
							if first, _ := inp[0].(map[string]any); first != nil {
								if cArr, _ := first["content"].([]any); len(cArr) > 0 {
									if c0, _ := cArr[0].(map[string]any); c0 != nil {
										if txt, ok := c0["text"].(string); ok {
											mu.Lock()
											sawDecodedText = txt
											mu.Unlock()
										}
									}
								}
							}
						}
					}
				}
				dec.Close()
			}
			mu.Lock()
			reqCount++
			n := reqCount
			sawHeaders = r.Header.Clone()
			sawEncoding = r.Header.Get("Content-Encoding")
			mu.Unlock()

			w.Header().Set("Content-Type", "text/event-stream")
			switch n {
			case 1:
				// Normal SSE with trailing blank line
				_, _ = w.Write([]byte(tocrCompletedSSEEvents("Hello", "resp_sse_1")))
			case 2:
				// No trailing blank line (#9047)
				_, _ = w.Write([]byte(strings.TrimSpace(tocrCompletedSSEEvents("NoBlank", "resp_sse_2"))))
			default:
				// Stays open after response.completed until client finishes or test releases keepOpenDone
				_, _ = w.Write([]byte(tocrCompletedSSEEvents("OpenBody", "resp_sse_3")))
				if fl, ok := w.(http.Flusher); ok {
					fl.Flush()
				}
				select {
				case <-keepOpenDone:
				case <-r.Context().Done():
				}
			}
		}))
		defer srv.Close()
		defer close(keepOpenDone)

		m := tocrModel(srv.URL + "/backend-api")
		largeText := strings.Repeat("compress me ", 200)
		var providerEvents []string
		var providerModels []*ai.Model
		sseTransport := ai.TransportSse
		res, err := StreamOpenaiCodexResponses(context.Background(), m, tocrUserCtx(largeText), &OpenAICodexResponsesOptions{
			StreamOptions: ai.StreamOptions{
				ApiKey:    &token,
				Transport: &sseTransport,
				SessionId: tocrStrPtr("sess-sse"),
				OnProviderStreamEvent: func(data any, evModel *ai.Model) error {
					if obj, ok := data.(*jsonx.Object); ok {
						typ, _ := obj.GetString("type")
						providerEvents = append(providerEvents, typ)
					}
					providerModels = append(providerModels, evModel)
					return nil
				},
			},
		}).Result(context.Background())
		if err != nil || res.StopReason != ai.StopReasonStop {
			t.Fatalf("case 1 failed: err=%v stop=%s msg=%v", err, res.StopReason, res.ErrorMessage)
		}
		if txt, _ := res.Content[0].(*ai.TextContent); txt == nil || txt.Text != "Hello" {
			t.Fatalf("unexpected text content: %+v", res.Content)
		}
		if sawEncoding != "zstd" || sawDecodedText != largeText {
			t.Fatalf("zstd decode check failed: encoding=%q decodedLen=%d wantLen=%d", sawEncoding, len(sawDecodedText), len(largeText))
		}
		if sawHeaders.Get("Authorization") != "Bearer "+token ||
			sawHeaders.Get("chatgpt-account-id") != "acc_test" ||
			sawHeaders.Get("OpenAI-Beta") != "responses=experimental" ||
			sawHeaders.Get("originator") != "pi" ||
			sawHeaders.Get("accept") != "text/event-stream" ||
			sawHeaders.Get("x-api-key") != "" ||
			sawHeaders.Get("session-id") != "sess-sse" ||
			sawHeaders.Get("x-client-request-id") != "sess-sse" {
			t.Fatalf("unexpected Codex SSE headers: %+v", sawHeaders)
		}
		wantUA, _ := ai.GetIdentityHeaders(ai.ClientIdentityTarget{Provider: string(m.Provider), BaseUrl: m.BaseUrl}, nil).Get("User-Agent")
		if wantUA != "" && sawHeaders.Get("User-Agent") != wantUA {
			t.Fatalf("expected User-Agent %q, got %q", wantUA, sawHeaders.Get("User-Agent"))
		}
		if len(providerEvents) != 5 || len(providerModels) != 5 || providerModels[0] != m {
			t.Fatalf("unexpected OnProviderStreamEvent calls: %v", providerEvents)
		}

		// Case 2: No trailing blank line
		res2, err := StreamOpenaiCodexResponses(context.Background(), m, tocrUserCtx("hi"), &OpenAICodexResponsesOptions{
			StreamOptions: ai.StreamOptions{ApiKey: &token, Transport: &sseTransport},
		}).Result(context.Background())
		if err != nil || res2.StopReason != ai.StopReasonStop {
			t.Fatalf("no-blank-line failed: err=%v stop=%s msg=%v", err, res2.StopReason, res2.ErrorMessage)
		}
		if txt, _ := res2.Content[0].(*ai.TextContent); txt == nil || txt.Text != "NoBlank" {
			t.Fatalf("expected NoBlank, got %+v", res2.Content)
		}

		// Case 3: Stays open after response.completed
		res3, err := StreamOpenaiCodexResponses(context.Background(), m, tocrUserCtx("hi"), &OpenAICodexResponsesOptions{
			StreamOptions: ai.StreamOptions{ApiKey: &token, Transport: &sseTransport},
		}).Result(context.Background())
		if err != nil || res3.StopReason != ai.StopReasonStop {
			t.Fatalf("open-body completion failed: err=%v stop=%s msg=%v", err, res3.StopReason, res3.ErrorMessage)
		}
		if txt, _ := res3.Content[0].(*ai.TextContent); txt == nil || txt.Text != "OpenBody" {
			t.Fatalf("expected OpenBody, got %+v", res3.Content)
		}
	})

	t.Run("clamps prompt_cache_key, omits cache key when cacheRetention=none, supports effort maps, toolChoice, strict, grammar, and SSE retries", func(t *testing.T) {
		token := tocrMockJWT("acc_cfg")
		var mu sync.Mutex
		var bodies []map[string]any
		var sawSessionHeader string
		var sawCustomUA string
		var attempt atomic.Int32

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rawBody, _ := io.ReadAll(r.Body)
			dec, _ := zstd.NewReader(nil)
			decompressed, _ := dec.DecodeAll(rawBody, nil)
			dec.Close()
			var b map[string]any
			_ = json.Unmarshal(decompressed, &b)
			mu.Lock()
			bodies = append(bodies, b)
			if r.Header.Get("session-id") != "" {
				sawSessionHeader = r.Header.Get("session-id")
			}
			if r.Header.Get("User-Agent") != "" {
				sawCustomUA = r.Header.Get("User-Agent")
			}
			mu.Unlock()

			n := attempt.Add(1)
			if n == 2 {
				w.Header().Set("Retry-After-Ms", "1")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(429)
				_, _ = w.Write([]byte(`{"error":{"code":"rate_limit_exceeded","message":"slow down"}}`))
				return
			}
			if n == 4 {
				w.Header().Set("Retry-After", "277403")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(429)
				_, _ = w.Write([]byte(`{"error":{"code":"rate_limit_exceeded","message":"way too long"}}`))
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(tocrCompletedSSEEvents("ok", "resp_cfg")))
		}))
		defer srv.Close()

		m := tocrModel(srv.URL + "/backend-api")
		m.Id = "gpt-5.5"
		m.ThinkingLevelMap = &ai.ThinkingLevelMap{
			Minimal: jsonx.Some("low"),
			Xhigh:   jsonx.Some("xhigh"),
		}
		m.Compat.V = &ai.OpenAIResponsesCompat{
			SupportsStrictMode:         tocrBoolPtr(true),
			SupportsOpenAIGrammarTools: tocrBoolPtr(true),
		}
		props := omap.NewMap[string, *typebox.Schema]()
		props.Set("code", typebox.String())
		schemaOpts := jsonx.NewObject()
		schemaOpts.Set("additionalProperties", false)
		lark := "start: /SELECT .+/"
		grammarTool := &ai.Tool{
			Name:        "run_sql",
			Description: "Run SQL",
			Parameters:  typebox.Object(props, schemaOpts),
			ConstrainedSampling: ai.ToolConstrainedSamplingValue{
				V: &ai.ConstrainedSamplingGrammar{
					Type:     "grammar",
					Variants: ai.GrammarVariants{OpenaiLark: &lark},
				},
			},
		}
		strictTool := &ai.Tool{
			Name:        "fn_strict",
			Description: "Strict fn",
			Parameters:  typebox.Object(props, schemaOpts),
			ConstrainedSampling: ai.ToolConstrainedSamplingValue{
				V: &ai.ConstrainedSamplingJsonSchema{Type: "json_schema", Strict: ai.ConstrainedSamplingStrictPrefer},
			},
		}

		sseTransport := ai.TransportSse
		longSess := strings.Repeat("🧩", 80)
		overrideHdrs := omap.NewMap[string, *string]()
		overrideHdrs.Set("User-Agent", tocrStrPtr("override-agent/1.0"))
		xhigh := ai.ThinkingLevelXhigh

		// Request 1: clamped prompt_cache_key + session-id header, User-Agent override, xhigh, toolChoice, strict + grammar
		res1, err := StreamOpenaiCodexResponses(context.Background(), m, tocrUserCtx("hi", grammarTool, strictTool), &OpenAICodexResponsesOptions{
			StreamOptions: ai.StreamOptions{
				ApiKey:     &token,
				Transport:  &sseTransport,
				SessionId:  &longSess,
				Headers:    overrideHdrs,
				MaxRetries: tocrIntPtr(1),
			},
			ReasoningEffort: &xhigh,
			ToolChoice:      "required",
		}).Result(context.Background())
		if err != nil || res1.StopReason != ai.StopReasonStop {
			t.Fatalf("req 1 failed: err=%v stop=%s msg=%v", err, res1.StopReason, res1.ErrorMessage)
		}
		if len([]rune(sawSessionHeader)) != 64 || sawCustomUA != "override-agent/1.0" {
			t.Fatalf("expected 64-rune clamped session-id (len=%d) and custom UA=%q", len([]rune(sawSessionHeader)), sawCustomUA)
		}
		b0 := bodies[0]
		if pck, _ := b0["prompt_cache_key"].(string); len([]rune(pck)) != 64 {
			t.Fatalf("expected 64-rune clamped prompt_cache_key, got len=%d", len([]rune(pck)))
		}
		if rMap, _ := b0["reasoning"].(map[string]any); rMap == nil || rMap["effort"] != "xhigh" {
			t.Fatalf("expected reasoning.effort=xhigh, got %#v", b0["reasoning"])
		}
		if b0["tool_choice"] != "required" {
			t.Fatalf("expected tool_choice=required, got %v", b0["tool_choice"])
		}
		rawTools, _ := json.Marshal(b0["tools"])
		if !strings.Contains(string(rawTools), `"type":"custom"`) || !strings.Contains(string(rawTools), `"strict":true`) {
			t.Fatalf("expected grammar custom tool + strict fn tool, got %s", string(rawTools))
		}

		// Request 2 (attempt 2 -> 429 Retry-After-Ms=1, attempt 3 -> 200) + minimal->low + cacheRetention=none
		noneRet := ai.CacheRetentionNone
		minLvl := ai.ThinkingLevelMinimal
		retries := 1
		res2, err := StreamOpenaiCodexResponses(context.Background(), m, tocrUserCtx("retry"), &OpenAICodexResponsesOptions{
			StreamOptions: ai.StreamOptions{
				ApiKey:         &token,
				Transport:      &sseTransport,
				SessionId:      tocrStrPtr("sess-omit"),
				CacheRetention: &noneRet,
				MaxRetries:     &retries,
			},
			ReasoningEffort: &minLvl,
		}).Result(context.Background())
		if err != nil || res2.StopReason != ai.StopReasonStop {
			t.Fatalf("retry request failed: err=%v stop=%s msg=%v", err, res2.StopReason, res2.ErrorMessage)
		}
		bLast := bodies[len(bodies)-1]
		if _, hasPCK := bLast["prompt_cache_key"]; hasPCK {
			t.Fatalf("expected prompt_cache_key omitted on CacheRetention=none, got %v", bLast["prompt_cache_key"])
		}
		if rMap, _ := bLast["reasoning"].(map[string]any); rMap == nil || rMap["effort"] != "low" {
			t.Fatalf("expected minimal mapped to effort=low, got %#v", bLast["reasoning"])
		}

		// Request 3 (attempt 4 -> retry-after exceeds maxRetryDelayMs=1000 -> fails cleanly without retry)
		beforeCount := attempt.Load()
		retries3 := 2
		res3, _ := StreamOpenaiCodexResponses(context.Background(), m, tocrUserCtx("fail delay"), &OpenAICodexResponsesOptions{
			StreamOptions: ai.StreamOptions{
				ApiKey:          &token,
				Transport:       &sseTransport,
				MaxRetries:      &retries3,
				MaxRetryDelayMs: tocrInt64Ptr(1000),
			},
		}).Result(context.Background())
		if res3.StopReason != ai.StopReasonError || attempt.Load() != beforeCount+1 {
			t.Fatalf("expected single failed attempt when retry-after exceeds maxRetryDelayMs, got stop=%s deltaAttempts=%d",
				res3.StopReason, attempt.Load()-beforeCount)
		}
	})
}

func TestOpenAICodexWebSocketPoolingAndFallbacks(t *testing.T) {
	t.Run("scopes pooled WS connections by account, reuses with delta continuation, expires by maxAge, and skips cache when cacheRetention=none", func(t *testing.T) {
		tocrResetPools()
		defer tocrResetPools()

		var mu sync.Mutex
		var wsPayloads [][]map[string]any

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "done")
			connIdx := 0
			mu.Lock()
			connIdx = len(wsPayloads)
			wsPayloads = append(wsPayloads, nil)
			mu.Unlock()

			for {
				_, raw, readErr := conn.Read(r.Context())
				if readErr != nil {
					return
				}
				var msg map[string]any
				_ = json.Unmarshal(raw, &msg)
				mu.Lock()
				wsPayloads[connIdx] = append(wsPayloads[connIdx], msg)
				turnNum := len(wsPayloads[connIdx])
				mu.Unlock()

				respID := "resp_ws_" + string(rune('a'+connIdx)) + "_" + string(rune('0'+turnNum))
				_ = tocrWriteWSJSON(r.Context(), conn, `{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_1","role":"assistant","content":[]}}`)
				_ = tocrWriteWSJSON(r.Context(), conn, `{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"ws-ok"}`)
				_ = tocrWriteWSJSON(r.Context(), conn, `{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"ws-ok"}]}}`)
				_ = tocrWriteWSJSON(r.Context(), conn, `{"type":"response.completed","response":{"id":"`+respID+`","status":"completed","usage":{"input_tokens":5,"output_tokens":2,"total_tokens":7}}}`)
			}
		}))
		defer srv.Close()

		m := tocrModel(srv.URL + "/backend-api")
		tok1 := tocrMockJWT("acc_1")
		tok2 := tocrMockJWT("acc_2")
		wsTransport := ai.TransportWebsocketCached
		sess := "sess-ws-pool"

		// Turn 1 (acc_1)
		ctx1 := tocrUserCtx("turn 1")
		res1, err := StreamOpenaiCodexResponses(context.Background(), m, ctx1, &OpenAICodexResponsesOptions{
			StreamOptions: ai.StreamOptions{ApiKey: &tok1, Transport: &wsTransport, SessionId: &sess},
		}).Result(context.Background())
		if err != nil || res1.StopReason != ai.StopReasonStop {
			t.Fatalf("acc_1 turn 1 failed: err=%v stop=%s msg=%v", err, res1.StopReason, res1.ErrorMessage)
		}

		// Turn 2 (acc_2 in same session creates separate pooled connection)
		res2, err := StreamOpenaiCodexResponses(context.Background(), m, tocrUserCtx("other acc"), &OpenAICodexResponsesOptions{
			StreamOptions: ai.StreamOptions{ApiKey: &tok2, Transport: &wsTransport, SessionId: &sess},
		}).Result(context.Background())
		if err != nil || res2.StopReason != ai.StopReasonStop {
			t.Fatalf("acc_2 failed: err=%v stop=%s msg=%v", err, res2.StopReason, res2.ErrorMessage)
		}

		// Turn 3 (acc_1 reuses conn 0 and sends delta continuation with store=false + previous_response_id)
		followUp := "turn 2 follow up"
		turn1User := &ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tocrStrPtr("turn 1")}, Timestamp: 1}
		ctx3 := ai.NormalizeContext(&ai.Context{
			SystemPrompt: tocrStrPtr("You are a helpful assistant."),
			Messages: ai.MessageList{
				turn1User,
				res1,
				&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: &followUp}, Timestamp: 2},
			},
		})
		res3, err := StreamOpenaiCodexResponses(context.Background(), m, ctx3, &OpenAICodexResponsesOptions{
			StreamOptions: ai.StreamOptions{ApiKey: &tok1, Transport: &wsTransport, SessionId: &sess},
		}).Result(context.Background())
		if err != nil || res3.StopReason != ai.StopReasonStop {
			t.Fatalf("acc_1 turn 3 reuse failed: err=%v stop=%s msg=%v", err, res3.StopReason, res3.ErrorMessage)
		}

		stats := GetOpenAICodexWebSocketDebugStats(sess)
		if stats == nil || stats.ConnectionsCreated != 2 || stats.ConnectionsReused != 1 || stats.DeltaRequests != 1 {
			t.Fatalf("expected ConnectionsCreated=2 ConnectionsReused=1 DeltaRequests=1, got %+v", stats)
		}
		mu.Lock()
		secondReqOnAcc1 := wsPayloads[0][1]
		mu.Unlock()
		if secondReqOnAcc1["previous_response_id"] != "resp_ws_a_1" || secondReqOnAcc1["store"] != false {
			t.Fatalf("expected previous_response_id=resp_ws_a_1 and store=false, got prev=%v store=%v",
				secondReqOnAcc1["previous_response_id"], secondReqOnAcc1["store"])
		}
		if inp, _ := secondReqOnAcc1["input"].([]any); len(inp) != 1 {
			t.Fatalf("expected delta input with 1 new message item, got %d items: %v", len(inp), inp)
		}

		// Max-age expiry test (55 minutes) opens fresh connection instead of reusing expired one
		ocrWSMu.Lock()
		if ae := ocrWebsocketSessionCache[sess]; ae != nil && ae["acc_1"] != nil {
			ae["acc_1"].createdAt -= ocrSessionWebsocketMaxAgeMs + 1000
		}
		ocrWSMu.Unlock()

		res4, err := StreamOpenaiCodexResponses(context.Background(), m, ctx3, &OpenAICodexResponsesOptions{
			StreamOptions: ai.StreamOptions{ApiKey: &tok1, Transport: &wsTransport, SessionId: &sess},
		}).Result(context.Background())
		if err != nil || res4.StopReason != ai.StopReasonStop {
			t.Fatalf("expired reuse fallback failed: err=%v stop=%s msg=%v", err, res4.StopReason, res4.ErrorMessage)
		}
		statsAfterExpire := GetOpenAICodexWebSocketDebugStats(sess)
		if statsAfterExpire.ConnectionsCreated != 3 {
			t.Fatalf("expected expired WS to open 3rd connection, got %+v", statsAfterExpire)
		}

		// CacheRetention=None forces one-shot uncached WS connections with no session cache/stats
		noneRet := ai.CacheRetentionNone
		sessOneShot := "sess-ws-oneshot"
		mu.Lock()
		connsBeforeOneShot := len(wsPayloads)
		mu.Unlock()
		for i := 0; i < 2; i++ {
			res5, err := StreamOpenaiCodexResponses(context.Background(), m, tocrUserCtx("one-shot"), &OpenAICodexResponsesOptions{
				StreamOptions: ai.StreamOptions{ApiKey: &tok1, Transport: &wsTransport, SessionId: &sessOneShot, CacheRetention: &noneRet},
			}).Result(context.Background())
			if err != nil || res5.StopReason != ai.StopReasonStop {
				t.Fatalf("one-shot WS #%d failed: err=%v stop=%s msg=%v", i+1, err, res5.StopReason, res5.ErrorMessage)
			}
		}
		mu.Lock()
		connsAfterOneShot := len(wsPayloads)
		mu.Unlock()
		if connsAfterOneShot-connsBeforeOneShot != 2 {
			t.Fatalf("expected 2 separate one-shot WS connections, got %d", connsAfterOneShot-connsBeforeOneShot)
		}
		if statsOS := GetOpenAICodexWebSocketDebugStats(sessOneShot); statsOS != nil {
			t.Fatalf("expected no session debug stats when CacheRetention=none, got %+v", statsOS)
		}
		ocrWSMu.Lock()
		pooledOneShot := len(ocrWebsocketSessionCache[sessOneShot])
		ocrWSMu.Unlock()
		if pooledOneShot != 0 {
			t.Fatalf("expected CacheRetention=none not to retain pooled WS, got %d entries", pooledOneShot)
		}
	})

	t.Run("recovers from websocket_connection_limit_reached and previous_response_not_found, and falls back to SSE on connect/pre-start idle timeout", func(t *testing.T) {
		tocrResetPools()
		defer tocrResetPools()

		tok := tocrMockJWT("acc_recover")
		autoTransport := ai.TransportAuto

		// 1. websocket_connection_limit_reached on 1st WS request, reconnect succeeds cleanly
		var wsConnNum atomic.Int32
		srvLimit := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "done")
			n := wsConnNum.Add(1)
			_, _, err = conn.Read(r.Context())
			if err != nil {
				return
			}
			if n == 1 {
				_ = tocrWriteWSJSON(r.Context(), conn, `{"type":"error","error":{"code":"websocket_connection_limit_reached","message":"limit reached"}}`)
				return
			}
			_ = tocrWriteWSJSON(r.Context(), conn, `{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"m1","role":"assistant","content":[]}}`)
			_ = tocrWriteWSJSON(r.Context(), conn, `{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"reconnected"}`)
			_ = tocrWriteWSJSON(r.Context(), conn, `{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"m1","role":"assistant","content":[{"type":"output_text","text":"reconnected"}]}}`)
			_ = tocrWriteWSJSON(r.Context(), conn, `{"type":"response.completed","response":{"id":"resp_rec","status":"completed"}}`)
		}))
		defer srvLimit.Close()

		resLim, err := StreamOpenaiCodexResponses(context.Background(), tocrModel(srvLimit.URL+"/backend-api"), tocrUserCtx("hi"), &OpenAICodexResponsesOptions{
			StreamOptions: ai.StreamOptions{ApiKey: &tok, Transport: &autoTransport, SessionId: tocrStrPtr("sess-limit")},
		}).Result(context.Background())
		if err != nil || resLim.StopReason != ai.StopReasonStop {
			t.Fatalf("expected reconnect recovery, got err=%v stop=%s msg=%v", err, resLim.StopReason, resLim.ErrorMessage)
		}
		if wsConnNum.Load() != 2 {
			t.Fatalf("expected 2 WS connections across limit reconnect, got %d", wsConnNum.Load())
		}

		// 2. previous_response_not_found recovery over WS (2nd req on cached socket fails with previous_response_not_found, retries full context on fresh WS)
		tocrResetPools()
		var prevMissStage atomic.Int32
		var fullCtxRecovered bool
		var mu sync.Mutex
		srvPrevMiss := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "done")
			for {
				_, raw, readErr := conn.Read(r.Context())
				if readErr != nil {
					return
				}
				var body map[string]any
				_ = json.Unmarshal(raw, &body)
				stage := prevMissStage.Add(1)
				if stage == 2 {
					_ = tocrWriteWSJSON(r.Context(), conn, `{"type":"error","error":{"code":"previous_response_not_found","message":"missing"}}`)
					return
				}
				if stage == 3 {
					mu.Lock()
					_, hasPrev := body["previous_response_id"]
					inp, _ := body["input"].([]any)
					fullCtxRecovered = !hasPrev && len(inp) >= 2
					mu.Unlock()
				}
				_ = tocrWriteWSJSON(r.Context(), conn, `{"type":"response.completed","response":{"id":"resp_p`+string(rune('0'+stage))+`","status":"completed"}}`)
			}
		}))
		defer srvPrevMiss.Close()

		mPrev := tocrModel(srvPrevMiss.URL + "/backend-api")
		sessPrev := "sess-prev-miss"
		c1 := tocrUserCtx("q1")
		r1, _ := StreamOpenaiCodexResponses(context.Background(), mPrev, c1, &OpenAICodexResponsesOptions{
			StreamOptions: ai.StreamOptions{ApiKey: &tok, Transport: &autoTransport, SessionId: &sessPrev},
		}).Result(context.Background())
		q2 := "q2"
		c2 := ai.NormalizeContext(&ai.Context{
			SystemPrompt: tocrStrPtr("You are a helpful assistant."),
			Messages: ai.MessageList{
				&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tocrStrPtr("q1")}, Timestamp: 1},
				r1,
				&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: &q2}, Timestamp: 2},
			},
		})
		r2, _ := StreamOpenaiCodexResponses(context.Background(), mPrev, c2, &OpenAICodexResponsesOptions{
			StreamOptions: ai.StreamOptions{ApiKey: &tok, Transport: &autoTransport, SessionId: &sessPrev},
		}).Result(context.Background())
		if r2.StopReason != ai.StopReasonStop || !fullCtxRecovered {
			t.Fatalf("expected full context retry after previous_response_not_found, got stop=%s msg=%v fullCtx=%v", r2.StopReason, r2.ErrorMessage, fullCtxRecovered)
		}

		// 3. Connect timeout triggers WebSocket connect timeout + SSE fallback under TransportAuto
		tocrResetPools()
		var sseFallbackHit atomic.Bool
		releaseSlowUpgrade := make(chan struct{})
		srvTimeout := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
				select {
				case <-time.After(80 * time.Millisecond):
				case <-releaseSlowUpgrade:
				case <-r.Context().Done():
				}
				return
			}
			sseFallbackHit.Store(true)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(tocrCompletedSSEEvents("sse-fallback", "resp_fb")))
		}))
		defer srvTimeout.Close()
		defer close(releaseSlowUpgrade)

		sessTimeout := "sess-ws-connect-timeout"
		resTimeout, err := StreamOpenaiCodexResponses(context.Background(), tocrModel(srvTimeout.URL+"/backend-api"), tocrUserCtx("hi"), &OpenAICodexResponsesOptions{
			StreamOptions: ai.StreamOptions{
				ApiKey:                    &tok,
				Transport:                 &autoTransport,
				SessionId:                 &sessTimeout,
				WebsocketConnectTimeoutMs: tocrInt64Ptr(15),
			},
		}).Result(context.Background())
		if err != nil || resTimeout.StopReason != ai.StopReasonStop || !sseFallbackHit.Load() {
			t.Fatalf("expected SSE fallback after connect timeout, got err=%v stop=%s msg=%v fallback=%v", err, resTimeout.StopReason, resTimeout.ErrorMessage, sseFallbackHit.Load())
		}
		if statsT := GetOpenAICodexWebSocketDebugStats(sessTimeout); statsT == nil || statsT.SseFallbacks != 1 || statsT.LastWebSocketError == nil || !strings.Contains(*statsT.LastWebSocketError, "WebSocket connect timeout after 15ms") {
			t.Fatalf("expected LastWebSocketError='WebSocket connect timeout after 15ms', got %+v", statsT)
		}

		// 4. Idle timeout AFTER stream start reports WebSocket idle timeout error
		tocrResetPools()
		wsOnly := ai.TransportWebsocket
		releaseMidIdle := make(chan struct{})
		srvIdleAfterStart := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "done")
			_, _, _ = conn.Read(r.Context())
			_ = tocrWriteWSJSON(r.Context(), conn, `{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"m1","role":"assistant","content":[]}}`)
			select {
			case <-time.After(100 * time.Millisecond):
			case <-releaseMidIdle:
			case <-r.Context().Done():
			}
		}))
		defer srvIdleAfterStart.Close()
		defer close(releaseMidIdle)

		resIdle, _ := StreamOpenaiCodexResponses(context.Background(), tocrModel(srvIdleAfterStart.URL+"/backend-api"), tocrUserCtx("hi"), &OpenAICodexResponsesOptions{
			StreamOptions: ai.StreamOptions{
				ApiKey:    &tok,
				Transport: &wsOnly,
				TimeoutMs: tocrInt64Ptr(15),
			},
		}).Result(context.Background())
		if resIdle.StopReason != ai.StopReasonError || resIdle.ErrorMessage == nil || !strings.Contains(*resIdle.ErrorMessage, "WebSocket idle timeout after 15ms") {
			t.Fatalf("expected WebSocket idle timeout after 15ms, got stop=%s msg=%v", resIdle.StopReason, resIdle.ErrorMessage)
		}
	})
}
