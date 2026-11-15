// Ported from packages/ai/test/cloudflare-workers-ai-system-one.test.ts (pi v1.5.2).

package api_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

func tcwasoModel(baseUrl string, inputs []ai.ModelModality) *ai.ClassifierModel {
	if len(inputs) == 0 {
		inputs = []ai.ModelModality{ai.ModelModalityText}
	}
	return &ai.ClassifierModel{
		BaseModel: ai.BaseModel{
			Id:       "@cf/system-one/clf",
			Name:     "System One",
			Api:      "cloudflare-workers-ai-system-one",
			Provider: "cloudflare-workers-ai",
			BaseUrl:  baseUrl,
			Input:    inputs,
			Cost:     ai.ModelCost{Input: 1.0, Output: 2.0},
		},
		Type: ai.ModelTypeClassifier,
	}
}

func tcwasoQuestions() *omap.Map[string, ai.ClassifierQuestion] {
	qs := omap.NewMap[string, ai.ClassifierQuestion]()
	qs.Set("b", &ai.ClassifierBoolQuestion{
		Type:         "bool",
		Instructions: "Is valid?",
		Criteria:     ai.ClassifierBoolCriteria{True: "yes", False: "no"},
	})
	choices := omap.NewMap[string, string]()
	choices.Set("low", "Low severity")
	choices.Set("high", "High severity")
	qs.Set("c", &ai.ClassifierChoiceQuestion{
		Type:         "choice",
		Instructions: "Pick severity",
		Criteria:     choices,
	})
	return qs
}

func TestCloudflareWorkersAiSystemOneClassify(t *testing.T) {
	var seenPath string
	var seenBody *jsonx.Object

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = jsonx.Unmarshal(raw, &seenBody)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"success": true,
			"result": {
				"answers": {
					"b": {"type": "noul", "noul": 0.9},
					"c": {"type": "choice", "choice": "high", "probabilities": {"low": 0.1, "high": 0.9}, "confidence": 0.8}
				},
				"usage": {"input_tokens": 40, "output_tokens": 10}
			}
		}`))
	}))
	defer srv.Close()

	apiKey := "cf-token"
	clf := api.CloudflareWorkersAISystemOneApi()
	res, err := clf.Classify(context.Background(), tcwasoModel(srv.URL+"/ai", nil), &ai.ClassifierContext{
		State:     jsonx.ObjectOf("step", "run"),
		Questions: tcwasoQuestions(),
	}, &ai.ClassifierOptions{
		ProviderRequestOptions: ai.ProviderRequestOptions[ai.ClassifierModel]{
			ApiKey: &apiKey,
		},
	})
	if err != nil {
		t.Fatalf("Classify error: %v", err)
	}
	if res.StopReason != ai.ClassifierStopReasonStop {
		t.Fatalf("unexpected stopReason %q: %s", res.StopReason, *res.ErrorMessage)
	}
	if seenPath != "/ai/run" {
		t.Fatalf("unexpected request path: %q", seenPath)
	}
	// Bool question is translated to "noul" on the wire and decoded back as bool.
	inputObj, _ := seenBody.GetObject("input")
	qsObj, _ := inputObj.GetObject("questions")
	qWire, _ := qsObj.GetObject("b")
	if qType, _ := qWire.GetString("type"); qType != "noul" {
		t.Fatalf("expected wire question type noul, got %v", qWire)
	}
	ansB, _ := res.Answers.Get("b")
	boolAns, ok := ansB.(*ai.ClassifierBoolAnswer)
	if !ok || boolAns.Type != "bool" || boolAns.Probability != 0.9 {
		t.Fatalf("unexpected bool answer: %#v", ansB)
	}
	ansC, _ := res.Answers.Get("c")
	choiceAns, ok := ansC.(*ai.ClassifierChoiceAnswer)
	if !ok || choiceAns.Choice != "high" {
		t.Fatalf("unexpected choice answer: %#v", ansC)
	}
	if res.Usage == nil || res.Usage.TotalTokens != 50 {
		t.Fatalf("unexpected usage: %+v", res.Usage)
	}
}
