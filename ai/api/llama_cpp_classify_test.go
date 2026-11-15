// Ported from packages/ai/test/llama-cpp-classify.test.ts (pi v1.5.2).

package api_test

import (
	"context"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

func tlccModel(baseUrl string) *ai.ClassifierModel {
	return &ai.ClassifierModel{
		BaseModel: ai.BaseModel{
			Id:       "test-qwen-model",
			Name:     "test-qwen-model",
			Api:      "llama-cpp-classify",
			Provider: "llama.cpp",
			BaseUrl:  baseUrl,
			Input:    []ai.ModelModality{ai.ModelModalityText},
		},
		Type: ai.ModelTypeClassifier,
	}
}

func tlccQuestions() *omap.Map[string, ai.ClassifierQuestion] {
	qs := omap.NewMap[string, ai.ClassifierQuestion]()
	qs.Set("yesno", &ai.ClassifierBoolQuestion{
		Type:         "bool",
		Instructions: "Should we proceed?",
		Criteria:     ai.ClassifierBoolCriteria{True: "safe", False: "unsafe"},
	})
	choices := omap.NewMap[string, string]()
	choices.Set("read", "Read-only action")
	choices.Set("write", "")
	qs.Set("kind", &ai.ClassifierChoiceQuestion{
		Type:         "choice",
		Instructions: "Action kind",
		Criteria:     choices,
	})
	qs.Set("risk", &ai.ClassifierScoreQuestion{
		Type:         "score",
		Instructions: "Risk level",
		Criteria:     []string{"low", "med", "high"},
	})
	return qs
}

func TestLlamaCppClassifyPureHelpers(t *testing.T) {
	if got := api.LlamaServerRoot("http://localhost:8080/v1/"); got != "http://localhost:8080" {
		t.Fatalf("LlamaServerRoot = %q", got)
	}
	if got := api.LlamaServerRoot("http://localhost:8080/prefix/v1"); got != "http://localhost:8080/prefix" {
		t.Fatalf("LlamaServerRoot prefix = %q", got)
	}

	clfCtx := &ai.ClassifierContext{
		State:     jsonx.ObjectOf("file", "main.go"),
		Questions: tlccQuestions(),
	}
	q, err := api.RenderQuestion(clfCtx, "kind")
	if err != nil {
		t.Fatalf("RenderQuestion error: %v", err)
	}
	if !slices.Equal(q.Labels, []string{"A", "B"}) || !slices.Equal(q.Keys, []string{"read", "write"}) {
		t.Fatalf("unexpected choice labels/keys: %+v", q)
	}
	if strings.Count(q.Content, `"State:"`) != 0 && strings.Count(q.Content, "State:\n") != 2 {
		t.Fatalf("expected repeated state blocks, got:\n%s", q.Content)
	}
	if !strings.Contains(q.Content, "A. read: Read-only action") || !strings.Contains(q.Content, "B. write\n") {
		t.Fatalf("unexpected rendered choice prompt:\n%s", q.Content)
	}

	probs := api.LabelProbabilities([]float64{-1, -1}, 1)
	if math.Abs(probs[0]-0.5) > 1e-9 || math.Abs(probs[1]-0.5) > 1e-9 {
		t.Fatalf("unexpected softmax: %+v", probs)
	}
	if c := api.PeakConfidence([]float64{0.5, 0.5}); math.Abs(c) > 1e-9 {
		t.Fatalf("expected 0 confidence for uniform binary, got %v", c)
	}

	scoreAns, err := api.AnswerFromProbabilities(&ai.ClassifierScoreQuestion{Type: "score"}, []string{"0", "1", "2"}, []float64{0.1, 0.2, 0.7})
	if err != nil {
		t.Fatal(err)
	}
	if sa := scoreAns.(*ai.ClassifierScoreAnswer); math.Abs(sa.Score-1.6) > 1e-9 {
		t.Fatalf("expected score 1.6, got %+v", sa)
	}
}

func TestLlamaCppClassifyEndToEndAndEscalation(t *testing.T) {
	tokenMap := map[string]int{
		"\n":    100,
		"\nYes": 201,
		"\nNo":  202,
		"\nA":   301,
		"\nB":   302,
		"\n0":   400,
		"\n1":   401,
		"\n2":   402,
	}
	var completionCalls atomic.Int32
	var payloadObsCount atomic.Int32
	var responseObsCount atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body *jsonx.Object
		_ = jsonx.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/tokenize":
			content, _ := body.GetString("content")
			if content == "\n" {
				_, _ = w.Write([]byte(`{"tokens":[100]}`))
				return
			}
			if id, ok := tokenMap[content]; ok {
				b, _ := jsonx.Marshal(map[string]any{"tokens": []int{100, id}})
				_, _ = w.Write(b)
				return
			}
			_, _ = w.Write([]byte(`{"tokens":[999, 998]}`))
		case "/apply-template":
			msgs, _ := body.GetArray("messages")
			userMsg, _ := msgs[1].(*jsonx.Object).GetString("content")
			b, _ := jsonx.Marshal(map[string]any{"prompt": "SYS:" + userMsg + "<think>"})
			_, _ = w.Write(b)
		case "/completion":
			completionCalls.Add(1)
			nProbsF, _ := body.GetNumber("n_probs")
			nProbs := int(nProbsF)
			prompt, _ := body.GetString("prompt")
			if !strings.HasSuffix(prompt, "<think></think>") {
				t.Fatalf("expected closed <think></think> block in prompt, got %q", prompt)
			}
			// Escalate when checking Yes/No on first call (depth 256 -> omit 202 so depth 4096 returns it).
			if strings.Contains(prompt, "Answer Yes or No.") && nProbs == 256 {
				_, _ = w.Write([]byte(`{"completion_probabilities":[{"top_logprobs":[{"id":201,"logprob":-0.1}]}]}`))
				return
			}
			b, _ := jsonx.Marshal(map[string]any{
				"completion_probabilities": []map[string]any{
					{
						"top_logprobs": []map[string]any{
							{"id": 201, "logprob": -0.2},
							{"id": 202, "logprob": -1.5},
							{"id": 301, "logprob": -0.5},
							{"id": 302, "logprob": -0.5},
							{"id": 400, "logprob": -2.0},
							{"id": 401, "logprob": -1.0},
							{"id": 402, "logprob": -0.2},
						},
					},
				},
			})
			_, _ = w.Write(b)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	clf := api.LlamaCppClassifyApi()
	model := tlccModel(srv.URL + "/v1")
	res, err := clf.Classify(context.Background(), model, &ai.ClassifierContext{
		State:     jsonx.ObjectOf("x", 1),
		Questions: tlccQuestions(),
	}, &ai.ClassifierOptions{
		ProviderRequestOptions: ai.ProviderRequestOptions[ai.ClassifierModel]{
			OnPayload: func(payload any, m *ai.ClassifierModel) (any, error) {
				payloadObsCount.Add(1)
				return nil, nil
			},
			OnResponse: func(resp *ai.ProviderResponse, m *ai.ClassifierModel) error {
				responseObsCount.Add(1)
				return nil
			},
		},
	})
	if err != nil {
		t.Fatalf("Classify failed: %v", err)
	}
	if res.StopReason != ai.ClassifierStopReasonStop {
		t.Fatalf("unexpected stopReason %q: %v", res.StopReason, res.ErrorMessage)
	}
	// 3 questions, first question escalated once (256 -> 4096) => 4 completion requests.
	if completionCalls.Load() != 4 || payloadObsCount.Load() != 4 || responseObsCount.Load() != 4 {
		t.Fatalf("unexpected completion/callback counts: completion=%d payload=%d response=%d",
			completionCalls.Load(), payloadObsCount.Load(), responseObsCount.Load())
	}
	ansChoice, _ := res.Answers.Get("kind")
	if ca := ansChoice.(*ai.ClassifierChoiceAnswer); ca.Choice != "read" {
		t.Fatalf("expected tie-break choice 'read', got %+v", ca)
	}

	// Rejects image inputs and non-positive temperature without hitting the server.
	resImg, _ := clf.Classify(context.Background(), model, &ai.ClassifierContext{
		State:     jsonx.NewObject(),
		Images:    []*ai.ImageContent{{Type: "image", MimeType: "image/png", Data: "AAAA"}},
		Questions: tlccQuestions(),
	}, nil)
	if resImg.StopReason != ai.ClassifierStopReasonError || resImg.ErrorMessage == nil || !strings.Contains(*resImg.ErrorMessage, "image input") {
		t.Fatalf("expected image input rejection, got %+v", resImg)
	}
	zeroTemp := 0.0
	resTemp, _ := clf.Classify(context.Background(), model, &ai.ClassifierContext{
		State:     jsonx.NewObject(),
		Questions: tlccQuestions(),
	}, &ai.ClassifierOptions{Temperature: &zeroTemp})
	if resTemp.StopReason != ai.ClassifierStopReasonError || resTemp.ErrorMessage == nil || !strings.Contains(*resTemp.ErrorMessage, "Temperature must be a positive number") {
		t.Fatalf("expected temperature error, got %+v", resTemp)
	}
}
