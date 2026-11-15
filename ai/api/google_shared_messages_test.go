// Ported from packages/ai/test/google-shared-{gemini3-unsigned-tool-call,image-tool-result-routing,signed-empty-blocks,retry} and google-thinking-*.test.ts (pi v1.5.2).

package api_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func tgsmStrPtr(s string) *string { return &s }

func tgsmModel(modelApi ai.KnownApi, provider, id, baseUrl string, tlm *ai.ThinkingLevelMap) *ai.Model {
	return &ai.Model{
		BaseModel: ai.BaseModel{
			Id:       id,
			Name:     id,
			Api:      string(modelApi),
			Provider: ai.ProviderId(provider),
			BaseUrl:  baseUrl,
			Input:    []ai.ModelModality{ai.ModelModalityText, ai.ModelModalityImage},
		},
		Reasoning:        true,
		ThinkingLevelMap: tlm,
		ContextWindow:    128000,
		MaxTokens:        8192,
	}
}

type tgsmStatusErr struct {
	status  int
	headers http.Header
	msg     string
}

func (e *tgsmStatusErr) Error() string                { return e.msg }
func (e *tgsmStatusErr) ProviderStatus() *int         { s := e.status; return &s }
func (e *tgsmStatusErr) ProviderHeaders() http.Header { return e.headers }

func TestGoogleSharedMessagesUnsignedImagesAndSignedEmptyBlocks(t *testing.T) {
	// 1. Gemini 3 unsigned tool call IDs & no synthetic skip_thought_signature_validator on GenAI.
	gen3 := tgsmModel(ai.KnownApiGoogleGenerativeAi, "google", "gemini-3-pro-preview", "https://example.com", nil)
	ctxUnsigned := ai.NormalizeContext(&ai.Context{
		Messages: ai.MessageList{
			&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tgsmStrPtr("Hi")}, Timestamp: 1},
			&ai.AssistantMessage{
				Role:     "assistant",
				Api:      ai.Api(gen3.Api),
				Provider: gen3.Provider,
				Model:    gen3.Id,
				Content: ai.ContentList{
					&ai.ToolCall{Type: "toolCall", Id: "call_1", Name: "bash", Arguments: jsonx.ObjectOf("command", "echo hi")},
					&ai.ToolCall{Type: "toolCall", Id: "call_2", Name: "bash", Arguments: jsonx.ObjectOf("command", "ls")},
				},
				StopReason: ai.StopReasonToolUse,
				Timestamp:  1,
			},
			&ai.ToolResultMessage{Role: "toolResult", ToolCallId: "call_1", ToolName: "bash", Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "hi"}}, Timestamp: 1},
			&ai.ToolResultMessage{Role: "toolResult", ToolCallId: "call_2", ToolName: "bash", Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "files"}}, Timestamp: 1},
		},
	})
	converted3 := api.ConvertGoogleMessages(gen3, ctxUnsigned)
	var fcIds, frIds []string
	var sawSkipSig bool
	for _, c := range converted3 {
		parts, _ := c.GetArray("parts")
		for _, p := range parts {
			po := p.(*jsonx.Object)
			if sig, _ := po.GetString("thoughtSignature"); sig == "skip_thought_signature_validator" {
				sawSkipSig = true
			}
			if fc, ok := po.GetObject("functionCall"); ok {
				if id, ok := fc.GetString("id"); ok {
					fcIds = append(fcIds, id)
				}
			}
			if fr, ok := po.GetObject("functionResponse"); ok {
				if id, ok := fr.GetString("id"); ok {
					frIds = append(frIds, id)
				}
			}
		}
	}
	if len(fcIds) != 2 || fcIds[0] != "call_1" || fcIds[1] != "call_2" {
		t.Fatalf("expected preserved functionCall ids, got %v", fcIds)
	}
	if len(frIds) != 2 || frIds[0] != "call_1" || frIds[1] != "call_2" {
		t.Fatalf("expected preserved functionResponse ids, got %v", frIds)
	}
	if sawSkipSig {
		t.Fatal("did not expect skip_thought_signature_validator for Google Gen AI unsigned tool call")
	}

	// 2. Image tool result routing: synthetic image turn on Gemini 2.5 vs nested functionResponse.parts on Gemini 3.
	makeImgTranscript := func(m *ai.Model) *ai.TranscriptContext {
		return ai.NormalizeContext(&ai.Context{
			Messages: ai.MessageList{
				&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tgsmStrPtr("read")}, Timestamp: 1},
				&ai.AssistantMessage{
					Role:       "assistant",
					Api:        ai.Api(m.Api),
					Provider:   m.Provider,
					Model:      m.Id,
					Content:    ai.ContentList{&ai.ToolCall{Type: "toolCall", Id: "call_img", Name: "read", Arguments: jsonx.ObjectOf("path", "image.png")}},
					StopReason: ai.StopReasonToolUse,
					Timestamp:  1,
				},
				&ai.ToolResultMessage{
					Role:       "toolResult",
					ToolCallId: "call_img",
					ToolName:   "read",
					Content:    ai.ContentList{&ai.ImageContent{Type: "image", Data: "abc", MimeType: "image/png"}},
					Timestamp:  1,
				},
			},
		})
	}
	gen25 := tgsmModel(ai.KnownApiGoogleGenerativeAi, "google", "gemini-2.5-flash", "https://example.com", nil)
	turns25 := api.ConvertGoogleMessages(gen25, makeImgTranscript(gen25))
	if len(turns25) != 4 {
		t.Fatalf("expected synthetic image user turn for Gemini 2.5 (4 turns total), got %d", len(turns25))
	}
	imgTurnParts, _ := turns25[3].GetArray("parts")
	if txt, _ := imgTurnParts[0].(*jsonx.Object).GetString("text"); txt != "Tool result image:" {
		t.Fatalf("expected synthetic image turn label, got %v", turns25[3])
	}

	turns3 := api.ConvertGoogleMessages(gen3, makeImgTranscript(gen3))
	if len(turns3) != 3 {
		t.Fatalf("expected nested image parts on Gemini 3 (3 turns), got %d", len(turns3))
	}
	p3, _ := turns3[2].GetArray("parts")
	fr3, _ := p3[0].(*jsonx.Object).GetObject("functionResponse")
	frParts, _ := fr3.GetArray("parts")
	if len(frParts) != 1 || !frParts[0].(*jsonx.Object).Has("inlineData") {
		t.Fatalf("expected inlineData inside nested functionResponse.parts, got %v", fr3)
	}

	// 3. Signed empty thinking/text blocks kept vs unsigned empty blocks dropped.
	sig := "AAAAAAAAAAAAAAAAAAAAAA=="
	signedCtx := ai.NormalizeContext(&ai.Context{
		Messages: ai.MessageList{
			&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tgsmStrPtr("Hi")}, Timestamp: 1},
			&ai.AssistantMessage{
				Role:     "assistant",
				Api:      ai.Api(gen3.Api),
				Provider: gen3.Provider,
				Model:    gen3.Id,
				Content: ai.ContentList{
					&ai.ThinkingContent{Type: "thinking", Thinking: "", ThinkingSignature: &sig},
					&ai.ThinkingContent{Type: "thinking", Thinking: ""},
					&ai.TextContent{Type: "text", Text: "", TextSignature: &sig},
					&ai.TextContent{Type: "text", Text: "   "},
					&ai.ToolCall{Type: "toolCall", Id: "call_1", Name: "bash", Arguments: jsonx.ObjectOf("command", "ls")},
				},
				StopReason: ai.StopReasonToolUse,
				Timestamp:  1,
			},
		},
	})
	signedTurns := api.ConvertGoogleMessages(gen3, signedCtx)
	modelParts, _ := signedTurns[1].GetArray("parts")
	if len(modelParts) != 3 {
		t.Fatalf("expected signed empty thinking + signed empty text + toolCall = 3 parts (unsigned dropped), got %d (%v)", len(modelParts), modelParts)
	}
}

func TestGoogleThinkingDetectionSignatureRetryAndLevelMaps(t *testing.T) {
	if !api.IsGoogleThinkingPart(jsonx.ObjectOf("thought", true)) {
		t.Fatal("thought=true must be thinking")
	}
	if api.IsGoogleThinkingPart(jsonx.ObjectOf("thoughtSignature", "opaque")) ||
		api.IsGoogleThinkingPart(jsonx.ObjectOf("thought", false, "thoughtSignature", "opaque")) {
		t.Fatal("thoughtSignature alone without thought=true must not be thinking")
	}

	sig1 := api.RetainGoogleThoughtSignature(nil, tgsmStrPtr("sig-1"))
	if sig1 == nil || *sig1 != "sig-1" {
		t.Fatalf("expected sig-1, got %v", sig1)
	}
	sig2 := api.RetainGoogleThoughtSignature(sig1, nil)
	if sig2 == nil || *sig2 != "sig-1" {
		t.Fatalf("expected retained sig-1, got %v", sig2)
	}
	sig3 := api.RetainGoogleThoughtSignature(sig2, tgsmStrPtr("sig-2"))
	if sig3 == nil || *sig3 != "sig-2" {
		t.Fatalf("expected updated sig-2, got %v", sig3)
	}

	// RetryGoogleRequest: retries 429 once when MaxRetries=1, skips 400 and MaxRetries=0.
	attempts := 0
	val, err := api.RetryGoogleRequest(context.Background(), func(ctx context.Context) (string, error) {
		attempts++
		if attempts == 1 {
			return "", &tgsmStatusErr{status: 429, msg: "got status: 429"}
		}
		return "ok", nil
	}, &api.GsGoogleRetryConfig{MaxRetries: 1})
	if err != nil || val != "ok" || attempts != 2 {
		t.Fatalf("expected 429 retry to succeed in 2 attempts, got val=%q err=%v attempts=%d", val, err, attempts)
	}

	attempts = 0
	_, err = api.RetryGoogleRequest(context.Background(), func(ctx context.Context) (string, error) {
		attempts++
		return "", &tgsmStatusErr{status: 400, msg: "bad request"}
	}, &api.GsGoogleRetryConfig{MaxRetries: 2})
	if err == nil || attempts != 1 {
		t.Fatalf("expected non-retryable 400 to stop after 1 attempt, got attempts=%d err=%v", attempts, err)
	}

	// ThinkingLevelMap capture on Google GenAI and Google Vertex.
	tlm := &ai.ThinkingLevelMap{
		Off:     jsonx.Some("off"),
		Minimal: jsonx.Some("MINIMAL"),
		Low:     jsonx.Some("LOW"),
		Medium:  jsonx.Some("MEDIUM"),
		High:    jsonx.Some("HIGH"),
	}
	captureGoogle := func(m *ai.Model, r *ai.ThinkingLevel) *jsonx.Object {
		var capObj *jsonx.Object
		transcript := ai.NormalizeContext(&ai.Context{
			Messages: ai.MessageList{&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tgsmStrPtr("Hi")}, Timestamp: 1}},
		})
		opts := &ai.SimpleStreamOptions{
			StreamOptions: ai.StreamOptions{
				ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
					ApiKey: tgsmStrPtr("test-key"),
					OnPayload: func(payload any, model *ai.Model) (any, error) {
						capObj = payload.(*jsonx.Object).Clone()
						return nil, errors.New("payload captured")
					},
				},
			},
			Reasoning: r,
		}
		if m.Api == string(ai.KnownApiGoogleGenerativeAi) {
			_, _ = api.StreamSimpleGoogleGenerativeAi(context.Background(), m, transcript, opts).Result(context.Background())
		} else {
			_, _ = api.StreamSimpleGoogleVertex(context.Background(), m, transcript, opts).Result(context.Background())
		}
		if capObj == nil {
			t.Fatalf("expected captured payload for %s", m.Api)
		}
		return capObj
	}

	lvlMed := ai.ThinkingLevelMedium
	for _, m := range []*ai.Model{
		tgsmModel(ai.KnownApiGoogleGenerativeAi, "google", "gemini-3-pro-preview", "https://example.invalid/v1beta", tlm),
		tgsmModel(ai.KnownApiGoogleVertex, "google-vertex", "gemini-3-pro-preview", "https://example.invalid/v1", tlm),
	} {
		p := captureGoogle(m, &lvlMed)
		cfg, _ := p.GetObject("config")
		tc, _ := cfg.GetObject("thinkingConfig")
		if inc, _ := tc.GetBool("includeThoughts"); !inc {
			t.Fatalf("%s: expected includeThoughts=true, got %v", m.Api, tc)
		}
		if tl, _ := tc.GetString("thinkingLevel"); tl != "MEDIUM" {
			t.Fatalf("%s: expected thinkingLevel=MEDIUM, got %v", m.Api, tc)
		}

		pOff := captureGoogle(m, nil)
		cfgOff, _ := pOff.GetObject("config")
		tcOff, _ := cfgOff.GetObject("thinkingConfig")
		if lvl, _ := tcOff.GetString("thinkingLevel"); lvl != "MINIMAL" && lvl != "LOW" {
			// For models whose disabled config uses thinkingBudget=0 vs minimal level:
			if budget, ok := tcOff.GetNumber("thinkingBudget"); !ok || budget != 0 {
				t.Fatalf("%s: unexpected disabled thinking config: %v", m.Api, tcOff)
			}
		}
	}

	// RetryGoogleRequest defaults MaxRetries to 0 when nil, ignores plain non-status substring errors, and honors x-should-retry=false.
	attempts = 0
	_, err = api.RetryGoogleRequest(context.Background(), func(context.Context) (string, error) {
		attempts++
		return "", &tgsmStatusErr{status: 429, msg: "got status: 429"}
	}, nil)
	if err == nil || attempts != 1 {
		t.Fatalf("expected nil RetryGoogleRequest config to default maxRetries=0, got attempts=%d err=%v", attempts, err)
	}

	attempts = 0
	_, err = api.RetryGoogleRequest(context.Background(), func(context.Context) (string, error) {
		attempts++
		return "", errors.New("503 overloaded resource_exhausted")
	}, &api.GsGoogleRetryConfig{MaxRetries: 2})
	if err == nil || attempts != 1 {
		t.Fatalf("expected plain substring error without status not to retry, got attempts=%d err=%v", attempts, err)
	}

	hdrNoRetry := make(http.Header)
	hdrNoRetry.Set("x-should-retry", "false")
	attempts = 0
	_, err = api.RetryGoogleRequest(context.Background(), func(context.Context) (string, error) {
		attempts++
		return "", &tgsmStatusErr{status: 429, headers: hdrNoRetry, msg: "429 do not retry"}
	}, &api.GsGoogleRetryConfig{MaxRetries: 2})
	if err == nil || attempts != 1 {
		t.Fatalf("expected x-should-retry=false not to retry, got attempts=%d err=%v", attempts, err)
	}

	// UsesGoogleThinkingLevel & RequiresGoogleToolCallId parity checks.
	for _, id := range []string{"gemini-3.1-pro-preview", "gemini-4-argon", "gemini-flash-latest", "gemini-flash-lite-latest", "gemma-4-27b", "gemma4"} {
		if !api.UsesGoogleThinkingLevel(&ai.Model{BaseModel: ai.BaseModel{Id: id}}) {
			t.Fatalf("expected UsesGoogleThinkingLevel=true for %q", id)
		}
	}
	for _, id := range []string{"gemini-3-custom", "gemini-2.5-flash"} {
		if api.UsesGoogleThinkingLevel(&ai.Model{BaseModel: ai.BaseModel{Id: id}}) {
			t.Fatalf("expected UsesGoogleThinkingLevel=false for %q", id)
		}
	}
	for _, id := range []string{"claude-sonnet-4-6", "gpt-oss-120b", "gemini-live-3-flash"} {
		if !api.RequiresGoogleToolCallId(id) {
			t.Fatalf("expected RequiresGoogleToolCallId=true for %q", id)
		}
	}
	for _, id := range []string{"gpt-4o", "gemini-2.5-pro"} {
		if api.RequiresGoogleToolCallId(id) {
			t.Fatalf("expected RequiresGoogleToolCallId=false for %q", id)
		}
	}

	// ConvertGoogleMessages: normalizes cross-model toolCall IDs (gpt-oss), keeps thoughtSignature on gemini-2.5,
	// and routes multimodal toolResult images inline for non-Gemini models (claude).
	gptOssModel := tgsmModel(ai.KnownApiGoogleVertex, "google-vertex", "gpt-oss-120b", "https://example.invalid", nil)
	rawBadID := "call/bad id?#" + strings.Repeat("x", 70)
	normOut := api.ConvertGoogleMessages(gptOssModel, ai.NormalizeContext(&ai.Context{
		Messages: ai.MessageList{
			&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tgsmStrPtr("go")}, Timestamp: 1},
			&ai.AssistantMessage{
				Role:       "assistant",
				Api:        ai.KnownApiAnthropicMessages,
				Provider:   "anthropic",
				Model:      "claude-sonnet-4-6",
				Content:    ai.ContentList{&ai.ToolCall{Type: "toolCall", Id: rawBadID, Name: "fn", Arguments: jsonx.NewObject()}},
				StopReason: ai.StopReasonToolUse,
				Timestamp:  2,
			},
			&ai.ToolResultMessage{Role: "toolResult", ToolCallId: rawBadID, ToolName: "fn", Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "ok"}}, Timestamp: 3},
		},
	}))
	fcNorm, _ := normOut[1].GetArray("parts")
	fcObj, _ := fcNorm[0].(*jsonx.Object).GetObject("functionCall")
	normID, _ := fcObj.GetString("id")
	if len(normID) != 64 || !strings.HasPrefix(normID, "call_bad_id__") {
		t.Fatalf("expected sanitized 64-char toolCall id, got %q (len=%d)", normID, len(normID))
	}
	frNorm, _ := normOut[2].GetArray("parts")
	frNormObj, _ := frNorm[0].(*jsonx.Object).GetObject("functionResponse")
	if frNormID, _ := frNormObj.GetString("id"); frNormID != normID {
		t.Fatalf("expected sanitized functionResponse id %q, got %q", normID, frNormID)
	}

	g25Model := tgsmModel(ai.KnownApiGoogleGenerativeAi, "google", "gemini-2.5-pro", "https://example.invalid", nil)
	sigB64 := "c2lnbmF0dXJl" // base64("signature")
	g25Out := api.ConvertGoogleMessages(g25Model, ai.NormalizeContext(&ai.Context{
		Messages: ai.MessageList{
			&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tgsmStrPtr("q")}, Timestamp: 1},
			&ai.AssistantMessage{
				Role:     "assistant",
				Api:      ai.KnownApiGoogleGenerativeAi,
				Provider: "google",
				Model:    "gemini-2.5-pro",
				Content: ai.ContentList{
					&ai.ThinkingContent{Type: "thinking", Thinking: "hmm", ThinkingSignature: &sigB64},
					&ai.ToolCall{Type: "toolCall", Id: "t1", Name: "fn", Arguments: jsonx.NewObject(), ThoughtSignature: &sigB64},
				},
				StopReason: ai.StopReasonToolUse,
				Timestamp:  2,
			},
		},
	}))
	g25Parts, _ := g25Out[1].GetArray("parts")
	if th, _ := g25Parts[0].(*jsonx.Object).GetBool("thought"); !th {
		t.Fatalf("expected thinking preserved for same provider+model, got %v", g25Parts[0])
	}
	if ts, _ := g25Parts[1].(*jsonx.Object).GetString("thoughtSignature"); ts != sigB64 {
		t.Fatalf("expected thoughtSignature echoed on gemini-2.5 toolCall, got %v", g25Parts[1])
	}

	claudeVertex := tgsmModel(ai.KnownApiGoogleVertex, "google-vertex", "claude-opus-4-6", "https://example.invalid", nil)
	claudeOut := api.ConvertGoogleMessages(claudeVertex, ai.NormalizeContext(&ai.Context{
		Messages: ai.MessageList{
			&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tgsmStrPtr("img")}, Timestamp: 1},
			&ai.AssistantMessage{
				Role:       "assistant",
				Api:        ai.KnownApiGoogleVertex,
				Provider:   "google-vertex",
				Model:      "claude-opus-4-6",
				Content:    ai.ContentList{&ai.ToolCall{Type: "toolCall", Id: "c1", Name: "read", Arguments: jsonx.NewObject()}},
				StopReason: ai.StopReasonToolUse,
				Timestamp:  2,
			},
			&ai.ToolResultMessage{
				Role:       "toolResult",
				ToolCallId: "c1",
				ToolName:   "read",
				Content:    ai.ContentList{&ai.ImageContent{Type: "image", MimeType: "image/png", Data: "iVBOR"}},
				Timestamp:  3,
			},
		},
	}))
	if len(claudeOut) != 3 {
		t.Fatalf("expected claude multimodal functionResponse without extra image turn, got %d turns", len(claudeOut))
	}
	cParts, _ := claudeOut[2].GetArray("parts")
	frObj, _ := cParts[0].(*jsonx.Object).GetObject("functionResponse")
	if frParts, ok := frObj.GetArray("parts"); !ok || len(frParts) != 1 {
		t.Fatalf("expected inline image inside functionResponse.parts for non-Gemini model, got %v", frObj)
	}
}
