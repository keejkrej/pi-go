// Ported from packages/evals/test/configured-runtime.test.ts (pi v1.0.0).

package suites_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/evals/suites"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func tcrWriteModelsJson(t *testing.T, payload any) string {
	t.Helper()
	dir := t.TempDir()
	encoded, err := jsonx.Marshal(payload)
	if err != nil {
		t.Fatalf("jsonx.Marshal failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "models.json"), append(encoded, '\n'), 0o600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	return dir
}

func tcrLoadRuntime(t *testing.T, agentDir string) *core.ModelRuntime {
	t.Helper()
	modelsPath := filepath.Join(agentDir, "models.json")
	authPath := filepath.Join(agentDir, "auth.json")
	modelsStorePath := filepath.Join(agentDir, "models-store.json")
	rt, err := core.CreateModelRuntime(context.Background(), &core.CreateModelRuntimeOptions{
		ModelsPath:        &modelsPath,
		AuthPath:          &authPath,
		ModelsStorePath:   &modelsStorePath,
		AllowModelNetwork: false,
	})
	if err != nil {
		t.Fatalf("CreateModelRuntime failed: %v", err)
	}
	t.Cleanup(rt.Close)
	return rt
}

func tcrAcmeModelsJson(server suites.AcmeServer, model suites.ModelFields) *jsonx.Object {
	return jsonx.ObjectOf(
		"providers", jsonx.ObjectOf(
			suites.OpenaiProviderId, jsonx.ObjectOf(
				"baseUrl", server.BaseUrl(),
				"api", "openai-completions",
				"apiKey", "$ACME_API_KEY",
				"models", []any{
					jsonx.ObjectOf(
						"id", model.Id,
						"name", model.Name,
						"reasoning", model.Reasoning,
						"input", model.Input,
						"cost", jsonx.ObjectOf(
							"input", model.Cost.Input,
							"output", model.Cost.Output,
							"cacheRead", model.Cost.CacheRead,
							"cacheWrite", model.Cost.CacheWrite,
						),
						"contextWindow", model.ContextWindow,
						"maxTokens", model.MaxTokens,
					),
				},
			),
		),
	)
}

func tcrBoolPtr(b bool) *bool { return &b }

func TestInspectProvider(t *testing.T) {
	server := suites.CreateAcmeServer(suites.AcmeServerModeOpenai)
	if err := server.Start(); err != nil {
		t.Fatalf("server.Start failed: %v", err)
	}
	t.Cleanup(func() {
		_ = server.Stop()
	})

	expectedModel := suites.ModelFields{
		Id:            suites.OpenaiModelId,
		Name:          "Acme Chat",
		Provider:      suites.OpenaiProviderId,
		Reasoning:     false,
		Input:         []string{"text"},
		Cost:          suites.ModelFieldsCost{Input: 0, Output: 0, CacheRead: 0, CacheWrite: 0},
		ContextWindow: 32768,
		MaxTokens:     4096,
	}

	probe := func(content, apiKey string) suites.ProviderScenario {
		maxTokens := 32
		return suites.ProviderScenario{
			ProviderId: suites.OpenaiProviderId,
			ModelId:    suites.OpenaiModelId,
			CreateContext: func() *ai.Context {
				txt := content
				return &ai.Context{
					Messages: ai.MessageList{
						&ai.UserMessage{
							Role:      "user",
							Content:   ai.UserMessageContent{Text: &txt},
							Timestamp: 0,
						},
					},
				}
			},
			Options: &ai.ModelsSimpleStreamOptions{
				SimpleStreamOptions: ai.SimpleStreamOptions{
					StreamOptions: ai.StreamOptions{
						ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
							Env: ai.ProviderEnv{"ACME_API_KEY": apiKey},
						},
						MaxTokens: &maxTokens,
					},
				},
			},
			ValidRequestReceived: server.ValidRequestReceived,
		}
	}

	t.Run("probes a models.json provider through ModelRuntime", func(t *testing.T) {
		server.Reset()
		rt := tcrLoadRuntime(t, tcrWriteModelsJson(t, tcrAcmeModelsJson(server, expectedModel)))
		got := suites.InspectProvider(context.Background(), rt, probe(suites.OpenaiProbePrompt, "resolved-acme-key"))
		want := suites.ProviderRuntimeOutput{
			Result: suites.ProviderRuntimeResult{
				ValidRequestReceived: tcrBoolPtr(true),
				Model:                &expectedModel,
				Response: &suites.ProviderRuntimeResponse{
					Text:         suites.OpenaiProbeResponse,
					StopReason:   "stop",
					InputTokens:  3,
					OutputTokens: 2,
				},
			},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got = %+v, want %+v", got, want)
		}
	})

	t.Run("keeps a non-probe completion distinct from a valid probe", func(t *testing.T) {
		server.Reset()
		rt := tcrLoadRuntime(t, tcrWriteModelsJson(t, tcrAcmeModelsJson(server, expectedModel)))
		got := suites.InspectProvider(context.Background(), rt, probe("hello", "resolved-acme-key"))
		want := suites.ProviderRuntimeOutput{
			Result: suites.ProviderRuntimeResult{
				ValidRequestReceived: tcrBoolPtr(false),
				Model:                &expectedModel,
				Response: &suites.ProviderRuntimeResponse{
					Text:         suites.OpenaiProbeResponse,
					StopReason:   "stop",
					InputTokens:  3,
					OutputTokens: 2,
				},
			},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got = %+v, want %+v", got, want)
		}
	})

	t.Run("returns a structured error when the configured model is missing", func(t *testing.T) {
		server.Reset()
		rt := tcrLoadRuntime(t, tcrWriteModelsJson(t, jsonx.ObjectOf("providers", jsonx.NewObject())))
		got := suites.InspectProvider(context.Background(), rt, probe(suites.OpenaiProbePrompt, "resolved-acme-key"))
		if got.Result.Error == nil || *got.Result.Error != "Model acme/acme-chat is unavailable after reload." {
			t.Fatalf("got error = %v, want missing model error", got.Result.Error)
		}
		if server.ValidRequestReceived() {
			t.Fatalf("expected ValidRequestReceived() = false")
		}
	})

	t.Run("returns a structured error when models.json cannot be parsed", func(t *testing.T) {
		server.Reset()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte("{"), 0o600); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}
		rt := tcrLoadRuntime(t, dir)
		got := suites.InspectProvider(context.Background(), rt, probe(suites.OpenaiProbePrompt, "resolved-acme-key"))
		if got.Result.Error == nil || !strings.Contains(*got.Result.Error, "Failed to parse models.json") {
			t.Fatalf("got error = %v, want Failed to parse models.json", got.Result.Error)
		}
		if server.ValidRequestReceived() {
			t.Fatalf("expected ValidRequestReceived() = false")
		}
	})

	t.Run("does not treat an unauthorized completion as a valid probe", func(t *testing.T) {
		server.Reset()
		rt := tcrLoadRuntime(t, tcrWriteModelsJson(t, tcrAcmeModelsJson(server, expectedModel)))
		got := suites.InspectProvider(context.Background(), rt, probe(suites.OpenaiProbePrompt, "wrong-key"))
		want := suites.ProviderRuntimeOutput{
			Result: suites.ProviderRuntimeResult{
				ValidRequestReceived: tcrBoolPtr(false),
				Model:                &expectedModel,
				Response: &suites.ProviderRuntimeResponse{
					Text:         "",
					StopReason:   "error",
					InputTokens:  0,
					OutputTokens: 0,
				},
			},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got = %+v, want %+v", got, want)
		}
	})
}

func TestInspectAddedModel(t *testing.T) {
	const provider = "openai"
	const modelId = "fixture-chat"
	addedModel := suites.ModelFields{
		Id:            modelId,
		Name:          "Fixture Chat",
		Provider:      provider,
		Reasoning:     true,
		Input:         []string{"text"},
		Cost:          suites.ModelFieldsCost{Input: 0, Output: 0, CacheRead: 0, CacheWrite: 0},
		ContextWindow: 32768,
		MaxTokens:     4096,
	}

	t.Run("loads an added model without dropping built-in models", func(t *testing.T) {
		dir := tcrWriteModelsJson(t, jsonx.ObjectOf(
			"providers", jsonx.ObjectOf(
				provider, jsonx.ObjectOf(
					"models", []any{
						jsonx.ObjectOf(
							"id", addedModel.Id,
							"name", addedModel.Name,
							"reasoning", addedModel.Reasoning,
							"input", addedModel.Input,
							"cost", jsonx.ObjectOf(
								"input", addedModel.Cost.Input,
								"output", addedModel.Cost.Output,
								"cacheRead", addedModel.Cost.CacheRead,
								"cacheWrite", addedModel.Cost.CacheWrite,
							),
							"contextWindow", addedModel.ContextWindow,
							"maxTokens", addedModel.MaxTokens,
						),
					},
				),
			),
		))
		got := suites.InspectAddedModel(context.Background(), tcrLoadRuntime(t, dir), provider, modelId)
		want := suites.AddedModelOutput{
			Result: suites.AddedModelResult{
				Model:                   &addedModel,
				ExistingModelsPreserved: tcrBoolPtr(true),
			},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got = %+v, want %+v", got, want)
		}
	})

	t.Run("returns a structured error when the added model is missing", func(t *testing.T) {
		dir := tcrWriteModelsJson(t, jsonx.ObjectOf("providers", jsonx.NewObject()))
		got := suites.InspectAddedModel(context.Background(), tcrLoadRuntime(t, dir), provider, modelId)
		if got.Result.Error == nil || *got.Result.Error != "Model openai/fixture-chat is unavailable after reload." {
			t.Fatalf("got error = %v, want missing model error", got.Result.Error)
		}
	})

	t.Run("returns a structured error when models.json cannot be parsed", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte("{"), 0o600); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}
		got := suites.InspectAddedModel(context.Background(), tcrLoadRuntime(t, dir), provider, modelId)
		if got.Result.Error == nil || !strings.Contains(*got.Result.Error, "Failed to parse models.json") {
			t.Fatalf("got error = %v, want Failed to parse models.json", got.Result.Error)
		}
	})
}
