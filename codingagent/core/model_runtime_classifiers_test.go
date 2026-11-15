// Ported from packages/coding-agent/test/model-runtime-classifiers.test.ts (pi v1.5.2).

package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

func tmrcStrPtr(s string) *string { return &s }
func tmrcBoolPtr(b bool) *bool    { return &b }

func tmrcNewRuntime(t *testing.T, creds ai.CredentialStore, modelsPath string) *ModelRuntime {
	t.Helper()
	t.Setenv("PI_OFFLINE", "1")
	t.Setenv("OPENROUTER_API_KEY", "")
	if creds == nil {
		creds = ai.NewInMemoryCredentialStore()
	}
	storePath := filepath.Join(filepath.Dir(modelsPath), "models-store.json")
	rt, err := CreateModelRuntime(t.Context(), &CreateModelRuntimeOptions{
		Credentials:     creds,
		ModelsPath:      &modelsPath,
		ModelsStorePath: &storePath,
		RefreshOnCreate: tmrcBoolPtr(true),
	})
	if err != nil {
		t.Fatalf("CreateModelRuntime: %v", err)
	}
	t.Cleanup(rt.Close)
	return rt
}

func TestModelRuntimeClassifiers(t *testing.T) {
	t.Run("separates builtin classifier models from chat models and applies OpenRouter baseUrl overrides", func(t *testing.T) {
		dir := t.TempDir()
		modelsPath := filepath.Join(dir, "models.json")
		raw, err := jsonx.MarshalIndent(jsonx.ObjectOf(
			"providers", jsonx.ObjectOf(
				"openrouter", jsonx.ObjectOf(
					"baseUrl", "https://proxy.example.com/openrouter/v1",
					"headers", jsonx.ObjectOf("x-or-proxy", "yes"),
				),
			),
		), "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(modelsPath, raw, 0o600); err != nil {
			t.Fatal(err)
		}

		creds := ai.NewInMemoryCredentialStore()
		_, _ = creds.Modify(t.Context(), "openrouter", func(ai.Credential) (ai.Credential, error) {
			return &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: tmrcStrPtr("sk-or-test")}, nil
		}, nil)
		rt := tmrcNewRuntime(t, creds, modelsPath)

		clfAny := rt.GetModelOfType(ai.ModelTypeClassifier, "openrouter", "~typesafe/jev-latest")
		clf, ok := clfAny.(*ai.ClassifierModel)
		if !ok || clf == nil {
			t.Fatalf("expected openrouter classifier ~typesafe/jev-latest, got %T (%v)", clfAny, clfAny)
		}
		if clf.BaseUrl != "https://proxy.example.com/openrouter/v1" {
			t.Fatalf("expected overridden BaseUrl on classifier, got %q", clf.BaseUrl)
		}

		// Classifier must NOT appear in chat-only GetModels().
		for _, chatM := range rt.GetModels(tmrcStrPtr("openrouter")) {
			if chatM.Id == "~typesafe/jev-latest" {
				t.Fatalf("classifier leaked into GetModels(): %+v", chatM)
			}
		}

		// Appears in GetAvailableOfType(ModelTypeClassifier) when openrouter has auth.
		availClf, err := rt.GetAvailableOfType(t.Context(), ai.ModelTypeClassifier, tmrcStrPtr("openrouter"), nil)
		if err != nil || len(availClf) == 0 {
			t.Fatalf("GetAvailableOfType failed: len=%d err=%v", len(availClf), err)
		}
	})

	t.Run("extension Classifier handler receives resolved auth, headers, and errors return ClassifierResult cleanly", func(t *testing.T) {
		dir := t.TempDir()
		modelsPath := filepath.Join(dir, "models.json")
		raw, _ := jsonx.MarshalIndent(jsonx.ObjectOf(
			"providers", jsonx.ObjectOf(
				"ext-clf", jsonx.ObjectOf(
					"headers", jsonx.ObjectOf("x-models-json", "from-file"),
				),
			),
		), "  ")
		if err := os.WriteFile(modelsPath, raw, 0o600); err != nil {
			t.Fatal(err)
		}

		rt := tmrcNewRuntime(t, nil, modelsPath)
		var sawKey *string
		var sawHdr ai.ProviderHeaders

		extHdrs := omap.NewMap[string, string]()
		extHdrs.Set("x-ext", "from-ext")

		if err := rt.RegisterProvider("ext-clf", ProviderConfigInput{
			BaseUrl: tmrcStrPtr("https://clf.example.com"),
			ApiKey:  tmrcStrPtr("$CLF_TEST_KEY"),
			Headers: extHdrs,
			Classifiers: map[string]ai.ProviderClassifier{
				"mock-clf-api": {
					Classify: func(_ context.Context, model *ai.ClassifierModel, _ *ai.ClassifierContext, opts *ai.ClassifierOptions) (*ai.ClassifierResult, error) {
						sawKey = opts.ApiKey
						sawHdr = opts.Headers
						ans := omap.NewMap[string, ai.ClassifierAnswer]()
						ans.Set("q1", &ai.ClassifierBoolAnswer{Type: "bool", Probability: 1})
						return &ai.ClassifierResult{
							Api:        ai.ClassifierApi(model.Api),
							Provider:   model.Provider,
							Model:      model.Id,
							Answers:    ans,
							StopReason: ai.ClassifierStopReasonStop,
							Timestamp:  1,
						}, nil
					},
				},
			},
			Models: []ProviderModelConfig{
				&ProviderClassifierModelConfig{
					ProviderModelConfigBase: ProviderModelConfigBase{Id: "clf-1", Name: "Classifier 1", Api: tmrcStrPtr("mock-clf-api")},
					Type:                    ai.ModelTypeClassifier,
					ContextWindow:           32000,
				},
			},
		}); err != nil {
			t.Fatalf("RegisterProvider: %v", err)
		}

		clf := rt.GetModelOfType(ai.ModelTypeClassifier, "ext-clf", "clf-1").(*ai.ClassifierModel)
		questions := omap.NewMap[string, ai.ClassifierQuestion]()
		questions.Set("q1", &ai.ClassifierBoolQuestion{Type: "bool", Instructions: "ok?", Criteria: ai.ClassifierBoolCriteria{True: "yes", False: "no"}})
		clfCtx := &ai.ClassifierContext{State: jsonx.ObjectOf("k", "v"), Questions: questions}

		// Missing env auth returns error stop reason instead of throwing Go error.
		t.Setenv("CLF_TEST_KEY", "")
		errRes, err := rt.Classify(t.Context(), clf, clfCtx, nil)
		if err != nil {
			t.Fatalf("Classify should return error ClassifierResult, got Go err %v", err)
		}
		if errRes.StopReason != "error" || errRes.ErrorMessage == nil || !strings.Contains(*errRes.ErrorMessage, "CLF_TEST_KEY") {
			t.Fatalf("expected error ClassifierResult mentioning CLF_TEST_KEY, got %+v", errRes)
		}

		t.Setenv("CLF_TEST_KEY", "clf-secret")
		okRes, err := rt.Classify(t.Context(), clf, clfCtx, &ai.ModelsClassifierOptions{
			ModelsRequestTransforms: ai.ModelsRequestTransforms{
				TransformHeaders: func(h ai.ProviderHeaders) (ai.ProviderHeaders, error) {
					out := omap.NewMap[string, *string]()
					if h != nil {
						for k, v := range h.All() {
							out.Set(k, v)
						}
					}
					v := "added"
					out.Set("x-transformed", &v)
					return out, nil
				},
			},
		})
		if err != nil {
			t.Fatalf("Classify: %v", err)
		}
		if okRes.StopReason != ai.ClassifierStopReasonStop || sawKey == nil || *sawKey != "clf-secret" {
			t.Fatalf("Classify mismatch: res=%+v key=%v", okRes, sawKey)
		}
		if v, _ := sawHdr.Get("x-models-json"); v == nil || *v != "from-file" {
			t.Fatalf("x-models-json = %v", v)
		}
		if v, _ := sawHdr.Get("x-ext"); v == nil || *v != "from-ext" {
			t.Fatalf("x-ext = %v", v)
		}
		if v, _ := sawHdr.Get("x-transformed"); v == nil || *v != "added" {
			t.Fatalf("x-transformed = %v", v)
		}
	})

	t.Run("HTTP server receives classifier BaseUrl override and headers", func(t *testing.T) {
		var gotAuth string
		var gotPath string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			gotPath = r.URL.Path
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"answers":{"q1":{"type":"bool","probability":1}}}`))
		}))
		defer srv.Close()

		dir := t.TempDir()
		modelsPath := filepath.Join(dir, "models.json")
		raw, _ := jsonx.MarshalIndent(jsonx.ObjectOf(
			"providers", jsonx.ObjectOf(
				"openrouter", jsonx.ObjectOf("baseUrl", srv.URL),
			),
		), "  ")
		if err := os.WriteFile(modelsPath, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		creds := ai.NewInMemoryCredentialStore()
		_, _ = creds.Modify(t.Context(), "openrouter", func(ai.Credential) (ai.Credential, error) {
			return &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: tmrcStrPtr("sk-or-http")}, nil
		}, nil)
		rt := tmrcNewRuntime(t, creds, modelsPath)

		clf := rt.GetModelOfType(ai.ModelTypeClassifier, "openrouter", "~typesafe/jev-latest").(*ai.ClassifierModel)
		questions := omap.NewMap[string, ai.ClassifierQuestion]()
		questions.Set("q1", &ai.ClassifierBoolQuestion{Type: "bool", Instructions: "pass?", Criteria: ai.ClassifierBoolCriteria{True: "yes", False: "no"}})
		_, _ = rt.Classify(t.Context(), clf, &ai.ClassifierContext{State: jsonx.ObjectOf(), Questions: questions}, &ai.ModelsClassifierOptions{
			ClassifierOptions: ai.ClassifierOptions{
				ProviderRequestOptions: ai.ProviderRequestOptions[ai.ClassifierModel]{
					HTTPClient: srv.Client(),
					MaxRetries: new(int),
				},
			},
		})
		if gotPath != "" && !strings.HasPrefix(gotAuth, "Bearer sk-or-http") {
			t.Fatalf("expected Bearer sk-or-http header at %s, got %q", gotPath, gotAuth)
		}
	})
}
