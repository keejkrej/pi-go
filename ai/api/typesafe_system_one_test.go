// Ported from packages/ai/test/typesafe-system-one.test.ts (pi v1.5.2).

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

func ttsoModel(baseUrl string, inputs []ai.ModelModality) *ai.ClassifierModel {
	if len(inputs) == 0 {
		inputs = []ai.ModelModality{ai.ModelModalityText}
	}
	return &ai.ClassifierModel{
		BaseModel: ai.BaseModel{
			Id:       "sys1-v1",
			Name:     "sys1-v1",
			Api:      "typesafe-system-one",
			Provider: "typesafe",
			BaseUrl:  baseUrl,
			Input:    inputs,
		},
		Type: ai.ModelTypeClassifier,
	}
}

func ttsoSingleBoolQuestion() *omap.Map[string, ai.ClassifierQuestion] {
	qs := omap.NewMap[string, ai.ClassifierQuestion]()
	qs.Set("safe", &ai.ClassifierBoolQuestion{
		Type:         "bool",
		Instructions: "Is it safe?",
		Criteria:     ai.ClassifierBoolCriteria{True: "safe", False: "unsafe"},
	})
	return qs
}

func TestTypesafeSystemOneClassifyWithImagesAndOptInHeader(t *testing.T) {
	var seenPath string
	var seenOptIn string
	var seenBody *jsonx.Object

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		seenOptIn = r.Header.Get("TypeSafe-Danger-Zone-Images")
		raw, _ := io.ReadAll(r.Body)
		_ = jsonx.Unmarshal(raw, &seenBody)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"answers": {"safe": {"type": "noul", "noul": 0.25}},
			"usage": {"input_tokens": 12, "output_tokens": 4}
		}`))
	}))
	defer srv.Close()

	apiKey := "ts-key"
	clf := api.TypesafeSystemOneApi()
	model := ttsoModel(srv.URL, []ai.ModelModality{ai.ModelModalityText, ai.ModelModalityImage})
	res, err := clf.Classify(context.Background(), model, &ai.ClassifierContext{
		State: jsonx.ObjectOf("cmd", "ls"),
		Images: []*ai.ImageContent{
			{Type: "image", MimeType: "image/png", Data: "AAAA"},
		},
		Questions: ttsoSingleBoolQuestion(),
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
	if seenPath != "/systemone" {
		t.Fatalf("expected /systemone path, got %q", seenPath)
	}
	if seenOptIn != "true" {
		t.Fatalf("expected TypeSafe-Danger-Zone-Images=true header, got %q", seenOptIn)
	}
	assets, _ := seenBody.GetObject("assets")
	if assets == nil || assets.Len() != 1 || !assets.Has("<image-1>") {
		t.Fatalf("expected <image-1> asset in body, got %v", seenBody)
	}
	ans, _ := res.Answers.Get("safe")
	if b, ok := ans.(*ai.ClassifierBoolAnswer); !ok || b.Probability != 0.25 {
		t.Fatalf("unexpected answer: %#v", ans)
	}
}
