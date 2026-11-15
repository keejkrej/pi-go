// Ported from packages/ai/test/openai-decisions.test.ts (pi v1.5.2).

package api

import (
	"bytes"
	"context"
	"io"
	"math"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

type todRoundTripFunc func(req *http.Request) (*http.Response, error)

func (f todRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func todPtr[T any](v T) *T { return &v }

func todModel() *ai.ClassifierModel {
	return &ai.ClassifierModel{
		BaseModel: ai.BaseModel{
			Id:       "gpt-6-luna",
			Name:     "GPT-6 Luna",
			Api:      string(ai.KnownClassifierApiOpenaiDecisions),
			Provider: "openai",
			BaseUrl:  "https://api.openai.com/v1",
			Input:    []ai.ModelModality{ai.ModelModalityText, ai.ModelModalityImage},
			Cost: ai.ModelCost{
				ModelCostRates: ai.ModelCostRates{
					Input:      0.1,
					Output:     0,
					CacheRead:  0,
					CacheWrite: 0,
				},
				Tiers: []ai.ModelCostTier{
					{
						InputTokensAbove: 272000,
						ModelCostRates: ai.ModelCostRates{
							Input:      0.2,
							Output:     0,
							CacheRead:  0,
							CacheWrite: 0,
						},
					},
				},
			},
		},
		Type:          ai.ModelTypeClassifier,
		ContextWindow: 922000,
	}
}

func todContext() *ai.ClassifierContext {
	choiceCriteria := omap.NewMap[string, string]()
	choiceCriteria.Set("success", "Successful")
	choiceCriteria.Set("failure", "")

	questions := omap.NewMap[string, ai.ClassifierQuestion]()
	questions.Set("category", &ai.ClassifierChoiceQuestion{
		Type:         "choice",
		Instructions: "Classify the message",
		Criteria:     choiceCriteria,
	})
	questions.Set("satisfaction", &ai.ClassifierScoreQuestion{
		Type:         "score",
		Instructions: "Score satisfaction",
		Criteria:     []string{"low", "neutral", "high"},
	})
	questions.Set("approved", &ai.ClassifierBoolQuestion{
		Type:         "bool",
		Instructions: "Does the user approve?",
		Criteria: ai.ClassifierBoolCriteria{
			True:  "Approval",
			False: "No approval",
		},
	})

	return &ai.ClassifierContext{
		State:     jsonx.ObjectOf("text", "The deployment succeeded, thank you."),
		Questions: questions,
	}
}

func todWireAnswers() []*jsonx.Object {
	return []*jsonx.Object{
		jsonx.ObjectOf(
			"type", "choice",
			"name", "category",
			"choice", "success",
			"probabilities", []*jsonx.Object{
				jsonx.ObjectOf("value", "success", "probability", 0.9),
				jsonx.ObjectOf("value", "failure", "probability", 0.1),
			},
			"confidence", 0.8,
		),
		jsonx.ObjectOf(
			"type", "score",
			"name", "satisfaction",
			"score", 1.8,
			"probabilities", []*jsonx.Object{
				jsonx.ObjectOf("value", 0, "label", "low", "probability", 0.05),
				jsonx.ObjectOf("value", 1, "label", "neutral", "probability", 0.1),
				jsonx.ObjectOf("value", 2, "label", "high", "probability", 0.85),
			},
			"confidence", 0.7,
		),
		jsonx.ObjectOf("type", "predicate", "name", "approved", "probability", 0.95),
	}
}

func todWireUsage() *jsonx.Object {
	return jsonx.ObjectOf(
		"input_tokens", 164,
		"input_tokens_details", jsonx.ObjectOf("cached_tokens", 0, "cache_write_tokens", 0),
		"output_tokens", 0,
		"output_tokens_details", jsonx.ObjectOf("reasoning_tokens", 0),
		"total_tokens", 164,
	)
}

func todImage() *ai.ImageContent {
	return &ai.ImageContent{Type: "image", Data: "aW1hZ2U=", MimeType: "image/png"}
}

func todJsonResponse(status int, payload any, headers ...map[string]string) (*http.Response, error) {
	raw, err := jsonx.Marshal(payload)
	if err != nil {
		return nil, err
	}
	h := make(http.Header)
	h.Set("content-type", "application/json")
	for _, extra := range headers {
		for k, v := range extra {
			h.Set(k, v)
		}
	}
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     h,
		Body:       io.NopCloser(bytes.NewReader(raw)),
	}, nil
}

func todTextResponse(status int, body string, headers ...map[string]string) *http.Response {
	h := make(http.Header)
	h.Set("content-type", "text/html")
	for _, extra := range headers {
		for k, v := range extra {
			h.Set(k, v)
		}
	}
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     h,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func todClient(fn todRoundTripFunc) *http.Client {
	return &http.Client{Transport: fn}
}

func todCloseTo(t *testing.T, got, want, tol float64, label string) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Fatalf("%s = %v, want %v (tol %v)", label, got, want, tol)
	}
}

func TestOpenAIDecisions(t *testing.T) {
	t.Run("maps questions to Decisions types and answers back by name", func(t *testing.T) {
		model := todModel()
		c := todContext()
		var capturedURL, capturedAuth string
		var capturedBody []byte
		calls := 0

		reversed := slices.Clone(todWireAnswers())
		slices.Reverse(reversed)

		client := todClient(func(req *http.Request) (*http.Response, error) {
			calls++
			capturedURL = req.URL.String()
			capturedAuth = req.Header.Get("authorization")
			var err error
			capturedBody, err = io.ReadAll(req.Body)
			if err != nil {
				return nil, err
			}
			return todJsonResponse(200, jsonx.ObjectOf(
				"model", "gpt-6-luna",
				"answers", reversed,
				"usage", todWireUsage(),
			))
		})

		result, err := OpenAIDecisionsApi().Classify(context.Background(), model, c, &ai.ClassifierOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.ClassifierModel]{
				ApiKey:     todPtr("secret"),
				HTTPClient: client,
			},
			Temperature: todPtr(1.5),
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if calls != 1 {
			t.Fatalf("HTTP calls = %d, want 1", calls)
		}
		if capturedURL != "https://api.openai.com/v1/decisions" {
			t.Fatalf("URL = %q, want https://api.openai.com/v1/decisions", capturedURL)
		}
		if capturedAuth != "Bearer secret" {
			t.Fatalf("Authorization = %q, want Bearer secret", capturedAuth)
		}

		parsedReq, err := jsonx.Parse(capturedBody)
		if err != nil {
			t.Fatalf("failed to parse captured body: %v", err)
		}
		stateJSON, _ := jsonx.Stringify(c.State)
		wantReq := jsonx.ObjectOf(
			"model", "gpt-6-luna",
			"input", stateJSON,
			"questions", []any{
				jsonx.ObjectOf(
					"type", "choice",
					"name", "category",
					"instructions", "Classify the message",
					"choices", []any{
						jsonx.ObjectOf("value", "success", "description", "Successful"),
						jsonx.ObjectOf("value", "failure"),
					},
				),
				jsonx.ObjectOf(
					"type", "score",
					"name", "satisfaction",
					"instructions", "Score satisfaction",
					"levels", []any{
						jsonx.ObjectOf("label", "low"),
						jsonx.ObjectOf("label", "neutral"),
						jsonx.ObjectOf("label", "high"),
					},
				),
				jsonx.ObjectOf(
					"type", "predicate",
					"name", "approved",
					"instructions", "Does the user approve?\n\nTrue means: Approval\nFalse means: No approval",
				),
			},
		)
		gotObj, ok := parsedReq.(*jsonx.Object)
		if !ok || !gotObj.Equal(wantReq) {
			gotStr, _ := jsonx.Stringify(parsedReq)
			wantStr, _ := jsonx.Stringify(wantReq)
			t.Fatalf("request body mismatch:\ngot:  %s\nwant: %s", gotStr, wantStr)
		}

		if result.StopReason != ai.ClassifierStopReasonStop {
			t.Fatalf("StopReason = %q, want stop (err: %v)", result.StopReason, result.ErrorMessage)
		}

		catAns, ok := result.Answers.Get("category")
		if !ok {
			t.Fatalf("missing answer for category")
		}
		catChoice, ok := catAns.(*ai.ClassifierChoiceAnswer)
		if !ok {
			t.Fatalf("category answer type = %T, want *ClassifierChoiceAnswer", catAns)
		}
		wantProbs := omap.NewMap[string, float64]()
		wantProbs.Set("success", 0.9)
		wantProbs.Set("failure", 0.1)
		if catChoice.Type != "choice" || catChoice.Choice != "success" || catChoice.Confidence != 0.8 ||
			!reflect.DeepEqual(catChoice.Probabilities.Keys(), wantProbs.Keys()) {
			t.Fatalf("unexpected category answer: %+v", catChoice)
		}
		for _, k := range wantProbs.Keys() {
			gv, _ := catChoice.Probabilities.Get(k)
			wv, _ := wantProbs.Get(k)
			if gv != wv {
				t.Fatalf("category probability[%q] = %v, want %v", k, gv, wv)
			}
		}

		satAns, ok := result.Answers.Get("satisfaction")
		if !ok {
			t.Fatalf("missing answer for satisfaction")
		}
		satScore, ok := satAns.(*ai.ClassifierScoreAnswer)
		if !ok || satScore.Type != "score" || satScore.Score != 1.8 || satScore.Confidence != 0.7 {
			t.Fatalf("unexpected satisfaction answer: %+v", satAns)
		}

		appAns, ok := result.Answers.Get("approved")
		if !ok {
			t.Fatalf("missing answer for approved")
		}
		appBool, ok := appAns.(*ai.ClassifierBoolAnswer)
		if !ok || appBool.Type != "bool" || appBool.Probability != 0.95 {
			t.Fatalf("unexpected approved answer: %+v", appAns)
		}

		if result.Usage == nil || result.Usage.Input != 164 || result.Usage.Output != 0 ||
			result.Usage.CacheRead != 0 || result.Usage.TotalTokens != 164 {
			t.Fatalf("unexpected usage: %+v", result.Usage)
		}
		todCloseTo(t, result.Usage.Cost.Total, 0.0000164, 1e-12, "usage.Cost.Total")
	})

	t.Run("prices long-context requests at the long-context input rate", func(t *testing.T) {
		result, err := ClassifyOpenaiDecisions(context.Background(), todModel(), todContext(), &ai.ClassifierOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.ClassifierModel]{
				ApiKey: todPtr("secret"),
				HTTPClient: todClient(func(req *http.Request) (*http.Response, error) {
					return todJsonResponse(200, jsonx.ObjectOf(
						"answers", todWireAnswers(),
						"usage", jsonx.ObjectOf("input_tokens", 300000, "output_tokens", 0),
					))
				}),
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Usage == nil {
			t.Fatalf("expected non-nil Usage")
		}
		todCloseTo(t, result.Usage.Cost.Total, 0.06, 1e-12, "long-context usage.Cost.Total")
	})

	t.Run("sends images after the state in one user message", func(t *testing.T) {
		c := todContext()
		img1 := todImage()
		img2 := &ai.ImageContent{Type: "image", Data: img1.Data, MimeType: "image/jpeg"}
		c.Images = []*ai.ImageContent{img1, img2}

		var capturedBody []byte
		result, err := ClassifyOpenaiDecisions(context.Background(), todModel(), c, &ai.ClassifierOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.ClassifierModel]{
				ApiKey: todPtr("secret"),
				HTTPClient: todClient(func(req *http.Request) (*http.Response, error) {
					var readErr error
					capturedBody, readErr = io.ReadAll(req.Body)
					if readErr != nil {
						return nil, readErr
					}
					return todJsonResponse(200, jsonx.ObjectOf("answers", todWireAnswers()))
				}),
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.StopReason != ai.ClassifierStopReasonStop {
			t.Fatalf("StopReason = %q, want stop (err: %v)", result.StopReason, result.ErrorMessage)
		}
		parsed, err := jsonx.Parse(capturedBody)
		if err != nil {
			t.Fatalf("parse body: %v", err)
		}
		obj := parsed.(*jsonx.Object)
		gotInput, _ := obj.Get("input")
		stateJSON, _ := jsonx.Stringify(c.State)
		wantInput := []any{
			jsonx.ObjectOf(
				"role", "user",
				"content", []any{
					jsonx.ObjectOf("type", "input_text", "text", stateJSON),
					jsonx.ObjectOf("type", "input_image", "image_url", "data:image/png;base64,aW1hZ2U="),
					jsonx.ObjectOf("type", "input_image", "image_url", "data:image/jpeg;base64,aW1hZ2U="),
				},
			),
		}
		gotStr, _ := jsonx.Stringify(gotInput)
		wantStr, _ := jsonx.Stringify(wantInput)
		if gotStr != wantStr {
			t.Fatalf("input mismatch:\ngot:  %s\nwant: %s", gotStr, wantStr)
		}
	})

	t.Run("rejects more than 128 images before sending", func(t *testing.T) {
		c := todContext()
		c.Images = make([]*ai.ImageContent, 129)
		for i := range c.Images {
			c.Images[i] = todImage()
		}
		calls := 0
		result, err := ClassifyOpenaiDecisions(context.Background(), todModel(), c, &ai.ClassifierOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.ClassifierModel]{
				ApiKey: todPtr("secret"),
				HTTPClient: todClient(func(req *http.Request) (*http.Response, error) {
					calls++
					return todJsonResponse(200, jsonx.ObjectOf("answers", todWireAnswers()))
				}),
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if calls != 0 {
			t.Fatalf("HTTP calls = %d, want 0", calls)
		}
		if result.StopReason != ai.ClassifierStopReasonError {
			t.Fatalf("StopReason = %q, want error", result.StopReason)
		}
		if result.ErrorMessage == nil || !strings.Contains(*result.ErrorMessage, "at most 128 images, got 129") {
			t.Fatalf("ErrorMessage = %v, want to contain image limit", result.ErrorMessage)
		}
	})

	t.Run("fails the result when a question is refused and keeps the billed usage", func(t *testing.T) {
		answers := todWireAnswers()
		result, err := ClassifyOpenaiDecisions(context.Background(), todModel(), todContext(), &ai.ClassifierOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.ClassifierModel]{
				ApiKey: todPtr("secret"),
				HTTPClient: todClient(func(req *http.Request) (*http.Response, error) {
					return todJsonResponse(200, jsonx.ObjectOf(
						"answers", []*jsonx.Object{
							answers[0],
							answers[1],
							jsonx.ObjectOf("type", "refusal", "name", "approved"),
						},
						"usage", todWireUsage(),
					))
				}),
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.StopReason != ai.ClassifierStopReasonError {
			t.Fatalf("StopReason = %q, want error", result.StopReason)
		}
		if result.Answers == nil || result.Answers.Len() != 0 {
			t.Fatalf("expected empty Answers on refusal, got %+v", result.Answers)
		}
		if result.ErrorMessage == nil || *result.ErrorMessage != "OpenAI Decisions refused to answer approved" {
			t.Fatalf("ErrorMessage = %v, want refusal message", result.ErrorMessage)
		}
		if result.Usage == nil || result.Usage.Input != 164 {
			t.Fatalf("expected billed usage Input=164, got %+v", result.Usage)
		}
	})

	t.Run("returns missing and mistyped answers as classifier errors", func(t *testing.T) {
		answers := todWireAnswers()
		missing, err := ClassifyOpenaiDecisions(context.Background(), todModel(), todContext(), &ai.ClassifierOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.ClassifierModel]{
				ApiKey: todPtr("secret"),
				HTTPClient: todClient(func(req *http.Request) (*http.Response, error) {
					return todJsonResponse(200, jsonx.ObjectOf("answers", answers[:2]))
				}),
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		mistyped, err := ClassifyOpenaiDecisions(context.Background(), todModel(), todContext(), &ai.ClassifierOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.ClassifierModel]{
				ApiKey: todPtr("secret"),
				HTTPClient: todClient(func(req *http.Request) (*http.Response, error) {
					return todJsonResponse(200, jsonx.ObjectOf(
						"answers", []*jsonx.Object{
							answers[0],
							answers[1],
							jsonx.ObjectOf("type", "score", "name", "approved"),
						},
					))
				}),
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if missing.StopReason != ai.ClassifierStopReasonError || missing.ErrorMessage == nil ||
			!strings.Contains(*missing.ErrorMessage, "did not return an answer for approved") {
			t.Fatalf("missing result = %+v (msg=%v)", missing, missing.ErrorMessage)
		}
		if mistyped.StopReason != ai.ClassifierStopReasonError || mistyped.ErrorMessage == nil ||
			!strings.Contains(*mistyped.ErrorMessage, "did not return a predicate answer for approved") {
			t.Fatalf("mistyped result = %+v (msg=%v)", mistyped, mistyped.ErrorMessage)
		}
	})

	t.Run("preserves prototype-sensitive question IDs in answers", func(t *testing.T) {
		var protoCtx ai.ClassifierContext
		rawCtx := `{"state":{},"questions":{"__proto__":{"type":"bool","instructions":"Is this true?","criteria":{"true":"Yes","false":"No"}}}}`
		if err := jsonx.Unmarshal([]byte(rawCtx), &protoCtx); err != nil {
			t.Fatalf("unmarshal prototype context: %v", err)
		}
		result, err := ClassifyOpenaiDecisions(context.Background(), todModel(), &protoCtx, &ai.ClassifierOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.ClassifierModel]{
				ApiKey: todPtr("secret"),
				HTTPClient: todClient(func(req *http.Request) (*http.Response, error) {
					return todJsonResponse(200, jsonx.ObjectOf(
						"answers", []*jsonx.Object{
							jsonx.ObjectOf("type", "predicate", "name", "__proto__", "probability", 0.75),
						},
					))
				}),
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.StopReason != ai.ClassifierStopReasonStop {
			t.Fatalf("StopReason = %q, want stop (err: %v)", result.StopReason, result.ErrorMessage)
		}
		if !result.Answers.Has("__proto__") {
			t.Fatalf("expected Answers to contain __proto__")
		}
		ans, _ := result.Answers.Get("__proto__")
		boolAns, ok := ans.(*ai.ClassifierBoolAnswer)
		if !ok || boolAns.Type != "bool" || boolAns.Probability != 0.75 {
			t.Fatalf("unexpected __proto__ answer: %+v", ans)
		}
	})

	t.Run("does not retry gateway timeouts and explains them instead of returning the HTML page", func(t *testing.T) {
		calls := 0
		result, err := ClassifyOpenaiDecisions(context.Background(), todModel(), todContext(), &ai.ClassifierOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.ClassifierModel]{
				ApiKey: todPtr("secret"),
				HTTPClient: todClient(func(req *http.Request) (*http.Response, error) {
					calls++
					return todTextResponse(504, "<!DOCTYPE html><html>Gateway time-out</html>", map[string]string{"retry-after-ms": "0"}), nil
				}),
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if calls != 1 {
			t.Fatalf("calls = %d, want 1 (504 must not retry)", calls)
		}
		if result.StopReason != ai.ClassifierStopReasonError {
			t.Fatalf("StopReason = %q, want error", result.StopReason)
		}
		if result.ErrorMessage == nil || !strings.Contains(*result.ErrorMessage, "OpenAI Decisions error (504): the request timed out at the gateway") {
			t.Fatalf("ErrorMessage = %v, want 504 timeout explanation", result.ErrorMessage)
		}
		if strings.Contains(*result.ErrorMessage, "<html>") {
			t.Fatalf("ErrorMessage leaked HTML: %q", *result.ErrorMessage)
		}
	})

	t.Run("still retries other server errors", func(t *testing.T) {
		attempt := 0
		result, err := ClassifyOpenaiDecisions(context.Background(), todModel(), todContext(), &ai.ClassifierOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.ClassifierModel]{
				ApiKey: todPtr("secret"),
				HTTPClient: todClient(func(req *http.Request) (*http.Response, error) {
					attempt++
					if attempt == 1 {
						return todTextResponse(503, "busy", map[string]string{"retry-after-ms": "0"}), nil
					}
					return todJsonResponse(200, jsonx.ObjectOf("answers", todWireAnswers()))
				}),
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if attempt != 2 {
			t.Fatalf("attempt = %d, want 2", attempt)
		}
		if result.StopReason != ai.ClassifierStopReasonStop {
			t.Fatalf("StopReason = %q, want stop (err: %v)", result.StopReason, result.ErrorMessage)
		}
	})

	t.Run("includes the API error body for other HTTP failures", func(t *testing.T) {
		result, err := ClassifyOpenaiDecisions(context.Background(), todModel(), todContext(), &ai.ClassifierOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.ClassifierModel]{
				ApiKey:     todPtr("secret"),
				MaxRetries: todPtr(0),
				HTTPClient: todClient(func(req *http.Request) (*http.Response, error) {
					return todJsonResponse(400, jsonx.ObjectOf(
						"error", jsonx.ObjectOf(
							"message", "Decision input exceeds the token limit.",
							"type", "invalid_request_error",
						),
					))
				}),
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.StopReason != ai.ClassifierStopReasonError {
			t.Fatalf("StopReason = %q, want error", result.StopReason)
		}
		if result.ErrorMessage == nil || !strings.Contains(*result.ErrorMessage, "OpenAI Decisions error (400)") ||
			!strings.Contains(*result.ErrorMessage, "Decision input exceeds the token limit.") {
			t.Fatalf("ErrorMessage = %v, want 400 error body", result.ErrorMessage)
		}
	})

	t.Run("rejects models for other classifier APIs and missing API keys", func(t *testing.T) {
		t.Setenv("OPENAI_API_KEY", "")
		calls := 0
		client := todClient(func(req *http.Request) (*http.Response, error) {
			calls++
			return todJsonResponse(200, jsonx.ObjectOf("answers", todWireAnswers()))
		})

		otherModel := todModel()
		otherModel.Api = "typesafe-system-one"
		otherApi, err := ClassifyOpenaiDecisions(context.Background(), otherModel, todContext(), &ai.ClassifierOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.ClassifierModel]{
				ApiKey:     todPtr("secret"),
				HTTPClient: client,
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		noKey, err := ClassifyOpenaiDecisions(context.Background(), todModel(), todContext(), &ai.ClassifierOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.ClassifierModel]{
				HTTPClient: client,
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if calls != 0 {
			t.Fatalf("calls = %d, want 0", calls)
		}
		if otherApi.ErrorMessage == nil || !strings.Contains(*otherApi.ErrorMessage, "Unsupported classifier API: typesafe-system-one") {
			t.Fatalf("otherApi.ErrorMessage = %v, want unsupported API message", otherApi.ErrorMessage)
		}
		if noKey.ErrorMessage == nil || !strings.Contains(*noKey.ErrorMessage, "No API key for provider: openai") {
			t.Fatalf("noKey.ErrorMessage = %v, want missing API key message", noKey.ErrorMessage)
		}
	})

	t.Run("marks canceled requests with aborted stopReason", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		result, err := ClassifyOpenaiDecisions(ctx, todModel(), todContext(), &ai.ClassifierOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.ClassifierModel]{
				ApiKey: todPtr("secret"),
				HTTPClient: todClient(func(req *http.Request) (*http.Response, error) {
					return nil, req.Context().Err()
				}),
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.StopReason != ai.ClassifierStopReasonAborted {
			t.Fatalf("StopReason = %q, want aborted", result.StopReason)
		}
		if result.Answers == nil || result.Answers.Len() != 0 {
			t.Fatalf("expected empty Answers on abort, got %+v", result.Answers)
		}
	})
}
