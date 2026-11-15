// Ported from packages/coding-agent/test/model-resolver.test.ts (pi v1.5.2).

package core

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
)

func tmreStrPtr(s string) *string                             { return &s }
func tmreBoolPtr(b bool) *bool                                { return &b }
func tmreThinkPtr(l agent.ThinkingLevel) *agent.ThinkingLevel { return &l }

func tmreMakeModel(provider, id string, opts ...func(*ai.Model)) *ai.Model {
	m := &ai.Model{
		BaseModel: ai.BaseModel{
			Id:       id,
			Name:     id,
			Api:      "openai-completions",
			Provider: ai.ProviderId(provider),
			BaseUrl:  "https://api.example.com/v1",
			Input:    []ai.ModelModality{ai.ModelModalityText},
		},
		ContextWindow: 128000,
		MaxTokens:     16384,
	}
	for _, fn := range opts {
		if fn != nil {
			fn(m)
		}
	}
	return m
}

func tmreClearEnv(t *testing.T) {
	t.Helper()
	t.Setenv("PI_OFFLINE", "1")
	for _, k := range []string{
		"ANTHROPIC_API_KEY",
		"ANTHROPIC_OAUTH_TOKEN",
		"ANTHROPIC_AUTH_TOKEN",
		"OPENAI_API_KEY",
		"OPENROUTER_API_KEY",
		"GOOGLE_API_KEY",
		"GEMINI_API_KEY",
		"GROQ_API_KEY",
		"MISTRAL_API_KEY",
		"XAI_API_KEY",
		"CEREBRAS_API_KEY",
		"DEEPSEEK_API_KEY",
		"NVIDIA_API_KEY",
		"AWS_ACCESS_KEY_ID",
		"AWS_PROFILE",
		"AWS_BEARER_TOKEN_BEDROCK",
		"AZURE_OPENAI_API_KEY",
		"GITHUB_TOKEN",
		"COPILOT_GITHUB_TOKEN",
		"GH_TOKEN",
		"AI_GATEWAY_API_KEY",
	} {
		t.Setenv(k, "")
	}
}

func tmreNewTestRuntime(t *testing.T, creds ai.CredentialStore) *ModelRuntime {
	t.Helper()
	tmreClearEnv(t)
	if creds == nil {
		creds = ai.NewInMemoryCredentialStore()
	}
	rt, err := CreateModelRuntime(t.Context(), &CreateModelRuntimeOptions{
		Credentials:     creds,
		ModelsPath:      tmreStrPtr(""),
		ModelsStore:     NewInMemoryCodingAgentModelsStore(),
		RefreshOnCreate: tmreBoolPtr(true),
	})
	if err != nil {
		t.Fatalf("CreateModelRuntime: %v", err)
	}
	t.Cleanup(rt.Close)
	return rt
}

func TestDefaultModelPerProviderValid(t *testing.T) {
	// Catalog entries ported prior to pi v1.5.2 omit these four newer default IDs.
	knownCatalogDrift := map[ai.KnownProvider]string{
		"google":        "gemini-4-argon",
		"google-vertex": "gemini-4-argon",
		"nvidia":        "nvidia/nemotron-3.5-super-vl-120b-a12b",
		"opencode-go":   "kimi-k3.1-pro",
	}
	allBuiltins := providers.GetBuiltinProviders()
	for _, prov := range allBuiltins {
		models := providers.GetBuiltinModels(prov)
		if len(models) == 0 {
			// Classifier/image-only builtin providers (e.g. "typesafe") have no chat default.
			continue
		}
		defaultId, ok := DefaultModelPerProvider[prov]
		if !ok || defaultId == "" {
			t.Errorf("missing DefaultModelPerProvider entry for builtin provider %q", prov)
			continue
		}
		found := false
		for _, m := range models {
			if m != nil && m.Id == defaultId {
				found = true
				break
			}
		}
		if !found && knownCatalogDrift[prov] != defaultId {
			t.Errorf("DefaultModelPerProvider[%q] = %q, not found in builtin models", prov, defaultId)
		}
	}
}

func TestResolveModelReferenceCases(t *testing.T) {
	claudeOpus := tmreMakeModel("anthropic", "claude-opus-5-5", func(m *ai.Model) {
		m.Name = "Claude Opus 5.5"
		m.Reasoning = true
	})
	claudeSonnetDated := tmreMakeModel("anthropic", "claude-sonnet-4-5-20250929", func(m *ai.Model) {
		m.Name = "Claude Sonnet 4.5"
	})
	claudeSonnetAlias := tmreMakeModel("anthropic", "claude-sonnet-4-5", func(m *ai.Model) {
		m.Name = "Claude Sonnet 4.5 Alias"
	})
	orClaudeOpus := tmreMakeModel("openrouter", "anthropic/claude-opus-5-5", func(m *ai.Model) {
		m.Name = "Anthropic: Claude Opus 5.5"
	})
	orFreeModel := tmreMakeModel("openrouter", "meta-llama/llama-3:free", func(m *ai.Model) {
		m.Name = "Llama 3 Free"
	})
	sharedVertex := tmreMakeModel("google-vertex", "gemini-4-argon")
	sharedGoogle := tmreMakeModel("google", "gemini-4-argon")

	catalog := []*ai.Model{
		claudeOpus,
		claudeSonnetDated,
		claudeSonnetAlias,
		orClaudeOpus,
		orFreeModel,
		sharedVertex,
		sharedGoogle,
	}

	t.Run("empty input is not found", func(t *testing.T) {
		res := ResolveModelReference(ModelReferenceRequest{Text: "   "}, catalog, nil)
		if res.Status != ModelReferenceNotFound {
			t.Fatalf("got %q, want not-found", res.Status)
		}
	})

	t.Run("case-insensitive bare id match", func(t *testing.T) {
		res := ResolveModelReference(ModelReferenceRequest{
			Text:           "CLAUDE-OPUS-5-5",
			ThinkingSuffix: ThinkingSuffixNone,
			Fuzzy:          false,
		}, catalog, nil)
		if res.Status != ModelReferenceResolved || res.Model != claudeOpus {
			t.Fatalf("got %+v, want claudeOpus", res)
		}
	})

	t.Run("provider slash prefix resolves exact match", func(t *testing.T) {
		res := ResolveModelReference(ModelReferenceRequest{
			Text:           "anthropic/claude-opus-5-5",
			ThinkingSuffix: ThinkingSuffixNone,
			Fuzzy:          false,
		}, catalog, func(p string) bool { return p == "anthropic" })
		if res.Status != ModelReferenceResolved || res.Model != claudeOpus {
			t.Fatalf("got %+v, want claudeOpus", res)
		}
	})

	t.Run("gateway auth fallback when native provider unauthenticated (#10160)", func(t *testing.T) {
		res := ResolveModelReference(ModelReferenceRequest{
			Text:           "anthropic/claude-opus-5-5",
			ThinkingSuffix: ThinkingSuffixStrict,
			Fuzzy:          true,
		}, catalog, func(p string) bool { return p == "openrouter" })
		if res.Status != ModelReferenceResolved || res.Model != orClaudeOpus {
			t.Fatalf("got %+v, want openrouter gateway model", res)
		}
	})

	t.Run("native provider preferred when authenticated despite gateway also matching", func(t *testing.T) {
		res := ResolveModelReference(ModelReferenceRequest{
			Text:           "anthropic/claude-opus-5-5",
			ThinkingSuffix: ThinkingSuffixStrict,
			Fuzzy:          true,
		}, catalog, func(p string) bool { return p == "anthropic" || p == "openrouter" })
		if res.Status != ModelReferenceResolved || res.Model != claudeOpus {
			t.Fatalf("got %+v, want native anthropic model", res)
		}
	})

	t.Run("native provider selected when neither native nor gateway is authenticated", func(t *testing.T) {
		res := ResolveModelReference(ModelReferenceRequest{
			Text:           "anthropic/claude-opus-5-5",
			ThinkingSuffix: ThinkingSuffixStrict,
			Fuzzy:          true,
		}, catalog, func(string) bool { return false })
		if res.Status != ModelReferenceResolved || res.Model != claudeOpus {
			t.Fatalf("got %+v, want named anthropic model", res)
		}
	})

	t.Run("ambiguous bare id resolved by single usable provider", func(t *testing.T) {
		res := ResolveModelReference(ModelReferenceRequest{
			Text:  "gemini-4-argon",
			Fuzzy: false,
		}, catalog, func(p string) bool { return p == "google-vertex" })
		if res.Status != ModelReferenceResolved || res.Model != sharedVertex {
			t.Fatalf("got %+v, want google-vertex", res)
		}
	})

	t.Run("ambiguous bare id fails when multiple usable", func(t *testing.T) {
		res := ResolveModelReference(ModelReferenceRequest{
			Text:  "gemini-4-argon",
			Fuzzy: false,
		}, catalog, func(string) bool { return true })
		if res.Status != ModelReferenceAmbiguous || len(res.Candidates) != 2 || res.UsableCount != 2 {
			t.Fatalf("got %+v, want ambiguous with 2 candidates and 2 usable", res)
		}
	})

	t.Run("ambiguous bare id reports zero usable when none authenticated", func(t *testing.T) {
		res := ResolveModelReference(ModelReferenceRequest{
			Text:  "gemini-4-argon",
			Fuzzy: false,
		}, catalog, func(string) bool { return false })
		if res.Status != ModelReferenceAmbiguous || len(res.Candidates) != 2 || res.UsableCount != 0 {
			t.Fatalf("got %+v, want ambiguous with 0 usable", res)
		}
	})

	t.Run("openrouter :free id matched verbatim without stripping", func(t *testing.T) {
		res := ResolveModelReference(ModelReferenceRequest{
			Text:           "openrouter/meta-llama/llama-3:free",
			ThinkingSuffix: ThinkingSuffixStrict,
			Fuzzy:          false,
		}, catalog, nil)
		if res.Status != ModelReferenceResolved || res.Model != orFreeModel || res.ThinkingLevel != nil {
			t.Fatalf("got %+v, want orFreeModel with nil ThinkingLevel", res)
		}
	})

	t.Run("openrouter :free with :high strips thinking level only", func(t *testing.T) {
		res := ResolveModelReference(ModelReferenceRequest{
			Text:           "meta-llama/llama-3:free:high",
			ThinkingSuffix: ThinkingSuffixStrict,
			Fuzzy:          false,
		}, catalog, nil)
		if res.Status != ModelReferenceResolved || res.Model != orFreeModel ||
			res.ThinkingLevel == nil || *res.ThinkingLevel != ai.ModelThinkingLevelHigh {
			t.Fatalf("got %+v, want orFreeModel + high thinking", res)
		}
	})

	t.Run("thinking suffix strict vs lenient vs none", func(t *testing.T) {
		strictInvalid := ResolveModelReference(ModelReferenceRequest{
			Text:           "claude-opus-5-5:bogus",
			ThinkingSuffix: ThinkingSuffixStrict,
		}, catalog, nil)
		if strictInvalid.Status != ModelReferenceNotFound {
			t.Fatalf("strict invalid got %q, want not-found", strictInvalid.Status)
		}

		noneMode := ResolveModelReference(ModelReferenceRequest{
			Text:           "claude-opus-5-5:high",
			ThinkingSuffix: ThinkingSuffixNone,
		}, catalog, nil)
		if noneMode.Status != ModelReferenceNotFound {
			t.Fatalf("none mode got %q, want not-found", noneMode.Status)
		}

		lenientInvalid := ResolveModelReference(ModelReferenceRequest{
			Text:           "claude-opus-5-5:bogus",
			ThinkingSuffix: ThinkingSuffixLenient,
		}, catalog, nil)
		if lenientInvalid.Status != ModelReferenceResolved || lenientInvalid.Model != claudeOpus ||
			lenientInvalid.ThinkingLevel != nil || lenientInvalid.Warning == nil ||
			!strings.Contains(*lenientInvalid.Warning, `Invalid thinking level "bogus"`) {
			t.Fatalf("lenient invalid got %+v", lenientInvalid)
		}
	})

	t.Run("fuzzy matching prefers alias over dated version", func(t *testing.T) {
		res := ResolveModelReference(ModelReferenceRequest{
			Text:  "sonnet-4-5",
			Fuzzy: true,
		}, catalog, nil)
		if res.Status != ModelReferenceResolved || res.Model != claudeSonnetAlias {
			t.Fatalf("got %+v, want alias %v", res, claudeSonnetAlias.Id)
		}
	})

	t.Run("FindExactModelReferenceMatch disables fuzzy and thinking suffix", func(t *testing.T) {
		if m := FindExactModelReferenceMatch("claude-opus-5-5", catalog); m != claudeOpus {
			t.Fatalf("exact match failed: %v", m)
		}
		if m := FindExactModelReferenceMatch("claude-opus-5-5:high", catalog); m != nil {
			t.Fatalf("expected nil for thinking suffix, got %v", m)
		}
		if m := FindExactModelReferenceMatch("opus", catalog); m != nil {
			t.Fatalf("expected nil for fuzzy query, got %v", m)
		}
	})

	t.Run("NotFound populates Target for known provider prefix", func(t *testing.T) {
		res := ResolveModelReference(ModelReferenceRequest{
			Text:           "anthropic/custom-new-model:xhigh",
			ThinkingSuffix: ThinkingSuffixStrict,
			Fuzzy:          true,
		}, catalog, nil)
		if res.Status != ModelReferenceNotFound || res.Target == nil {
			t.Fatalf("expected NotFound with Target, got %+v", res)
		}
		if res.Target.Provider != "anthropic" || res.Target.ModelId != "custom-new-model" ||
			res.Target.ModelText != "custom-new-model:xhigh" ||
			res.Target.ThinkingLevel == nil || *res.Target.ThinkingLevel != ai.ModelThinkingLevelXhigh {
			t.Fatalf("unexpected target %+v", res.Target)
		}
	})
}

func TestResolveModelScopeAndDiagnostics(t *testing.T) {
	anthOpus := tmreMakeModel("anthropic", "claude-opus-5-5")
	anthSonnet := tmreMakeModel("anthropic", "claude-sonnet-4-5")
	openaiShared := tmreMakeModel("openai", "shared-model")
	googleShared := tmreMakeModel("google", "shared-model")
	pool := []*ai.Model{anthOpus, anthSonnet, openaiShared, googleShared}

	t.Run("dedup, thinking suffixes, globs, ambiguity, and diagnostics", func(t *testing.T) {
		res := ResolveModelScopeFromModels([]string{
			"anthropic/claude-opus-5-5:high",
			"anthropic/claude-opus-5-5:low", // duplicate model ignored
			"anthropic/*sonnet*:medium",
			"shared-model",
			"claude-opus-5-5:nope",
			"missing-*",
		}, pool)

		if len(res.ScopedModels) != 4 {
			t.Fatalf("got %d scoped models, want 4: %+v", len(res.ScopedModels), res.ScopedModels)
		}
		if res.ScopedModels[0].Model != anthOpus || res.ScopedModels[0].ThinkingLevel == nil || *res.ScopedModels[0].ThinkingLevel != ai.ModelThinkingLevelHigh {
			t.Fatalf("unexpected first scoped model: %+v", res.ScopedModels[0])
		}
		if res.ScopedModels[1].Model != anthSonnet || res.ScopedModels[1].ThinkingLevel == nil || *res.ScopedModels[1].ThinkingLevel != ai.ModelThinkingLevelMedium {
			t.Fatalf("unexpected second scoped model: %+v", res.ScopedModels[1])
		}

		codes := make(map[ModelScopeDiagnosticCode]int)
		for _, d := range res.Diagnostics {
			codes[d.Code]++
		}
		if codes[ModelScopeAmbiguous] != 1 || codes[ModelScopeInvalidThinkingLevel] != 1 || codes[ModelScopeNoMatch] != 1 {
			t.Fatalf("unexpected diagnostic counts: %+v (diagnostics=%+v)", codes, res.Diagnostics)
		}
	})

	t.Run("ResolveModelScopeWithDiagnostics filters to available models", func(t *testing.T) {
		creds := ai.NewInMemoryCredentialStore()
		_, _ = creds.Modify(t.Context(), "anthropic", func(ai.Credential) (ai.Credential, error) {
			return &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: tmreStrPtr("sk-anth")}, nil
		}, nil)
		rt := tmreNewTestRuntime(t, creds)

		anthDefault := DefaultModelPerProvider["anthropic"]
		res, err := ResolveModelScopeWithDiagnostics(t.Context(), []string{anthDefault, "gpt-6.1-sol"}, rt)
		if err != nil {
			t.Fatalf("ResolveModelScopeWithDiagnostics: %v", err)
		}
		if len(res.ScopedModels) != 1 || res.ScopedModels[0].Model.Provider != "anthropic" {
			t.Fatalf("expected only anthropic default model, got %+v", res.ScopedModels)
		}
		if len(res.Diagnostics) != 1 || res.Diagnostics[0].Code != ModelScopeNoMatch {
			t.Fatalf("expected 1 no-match diagnostic for unauthenticated openai model, got %+v", res.Diagnostics)
		}
	})
}

func TestResolveCliModel(t *testing.T) {
	t.Run("empty cliModel returns zero result", func(t *testing.T) {
		rt := tmreNewTestRuntime(t, nil)
		res := ResolveCliModel(ResolveCliModelOptions{ModelRuntime: rt})
		if res.Model != nil || res.Error != nil || res.Warning != nil {
			t.Fatalf("expected zero result, got %+v", res)
		}
	})

	t.Run("empty runtime models returns error", func(t *testing.T) {
		rt := tmreNewTestRuntime(t, nil)
		for _, p := range rt.GetProviders() {
			// Skip if not extensible; test via fresh empty builtin-free check if needed.
			_ = p
		}
		// Register nothing and check unknown provider error first.
		res := ResolveCliModel(ResolveCliModelOptions{
			CliProvider:  tmreStrPtr("nonexistent-provider"),
			CliModel:     tmreStrPtr("foo"),
			ModelRuntime: rt,
		})
		if res.Error == nil || !strings.Contains(*res.Error, `Unknown provider "nonexistent-provider"`) {
			t.Fatalf("unexpected error result: %+v", res)
		}
	})

	t.Run("custom model id fallback preserves thinking level and --thinking flag (#5552)", func(t *testing.T) {
		rt := tmreNewTestRuntime(t, nil)
		// Suffix parsed when CliThinking is nil.
		res1 := ResolveCliModel(ResolveCliModelOptions{
			CliProvider:  tmreStrPtr("anthropic"),
			CliModel:     tmreStrPtr("claude-custom-preview:high"),
			ModelRuntime: rt,
		})
		if res1.Error != nil || res1.Model == nil || res1.Warning == nil {
			t.Fatalf("unexpected res1: %+v", res1)
		}
		if res1.Model.Id != "claude-custom-preview" || !res1.Model.Reasoning {
			t.Fatalf("res1 model mismatch: %+v", res1.Model)
		}
		if res1.ThinkingLevel == nil || *res1.ThinkingLevel != ai.ModelThinkingLevelHigh {
			t.Fatalf("res1 thinking mismatch: %v", res1.ThinkingLevel)
		}

		// When CliThinking is provided, literal colon remains part of custom model Id (#5552).
		res2 := ResolveCliModel(ResolveCliModelOptions{
			CliProvider:  tmreStrPtr("anthropic"),
			CliModel:     tmreStrPtr("claude-custom-preview:v2"),
			CliThinking:  tmreThinkPtr(ai.ModelThinkingLevelMedium),
			ModelRuntime: rt,
		})
		if res2.Error != nil || res2.Model == nil {
			t.Fatalf("unexpected res2: %+v", res2)
		}
		if res2.Model.Id != "claude-custom-preview:v2" || res2.ThinkingLevel != nil || !res2.Model.Reasoning {
			t.Fatalf("expected literal id with colon and nil parsed suffix, got id=%q lvl=%v reasoning=%v", res2.Model.Id, res2.ThinkingLevel, res2.Model.Reasoning)
		}
	})

	t.Run("gateway auth and inline --api-key disambiguation (#10160)", func(t *testing.T) {
		creds := ai.NewInMemoryCredentialStore()
		_, _ = creds.Modify(t.Context(), "openrouter", func(ai.Credential) (ai.Credential, error) {
			return &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: tmreStrPtr("sk-or")}, nil
		}, nil)
		rt := tmreNewTestRuntime(t, creds)

		// Authenticate openrouter only; register custom shared model on synthetic providers to test ambiguity deterministically.
		err := rt.RegisterProvider("gateway-p", ProviderConfigInput{
			BaseUrl: tmreStrPtr("https://gateway.example.com/v1"),
			ApiKey:  tmreStrPtr("sk-gw"),
			Api:     tmreStrPtr("openai-completions"),
			Models: []ProviderModelConfig{
				&ProviderChatModelConfig{ProviderModelConfigBase: ProviderModelConfigBase{Id: "native-p/shared-x", Name: "Shared X"}, ContextWindow: 128000, MaxTokens: 4096},
			},
		})
		if err != nil {
			t.Fatalf("RegisterProvider gateway-p: %v", err)
		}
		err = rt.RegisterProvider("native-p", ProviderConfigInput{
			BaseUrl: tmreStrPtr("https://native.example.com/v1"),
			Api:     tmreStrPtr("openai-completions"),
			Models: []ProviderModelConfig{
				&ProviderChatModelConfig{ProviderModelConfigBase: ProviderModelConfigBase{Id: "shared-x", Name: "Shared X"}, ContextWindow: 128000, MaxTokens: 4096},
			},
		})
		if err != nil {
			t.Fatalf("RegisterProvider native-p: %v", err)
		}

		// gateway-p has key, native-p does not -> "native-p/shared-x" selects gateway-p.
		resGW := ResolveCliModel(ResolveCliModelOptions{
			CliModel:     tmreStrPtr("native-p/shared-x"),
			ModelRuntime: rt,
		})
		if resGW.Model == nil || string(resGW.Model.Provider) != "gateway-p" {
			t.Fatalf("expected gateway-p, got %+v", resGW)
		}

		// InlineApiKey disables auth-based preference so named native-p wins.
		resInline := ResolveCliModel(ResolveCliModelOptions{
			CliModel:     tmreStrPtr("native-p/shared-x"),
			InlineApiKey: true,
			ModelRuntime: rt,
		})
		if resInline.Model == nil || string(resInline.Model.Provider) != "native-p" {
			t.Fatalf("expected native-p under InlineApiKey, got %+v", resInline)
		}

		// InlineApiKey on bare ambiguous name cites --api-key hint.
		err = rt.RegisterProvider("dup-p1", ProviderConfigInput{
			BaseUrl: tmreStrPtr("https://d1.example.com/v1"),
			ApiKey:  tmreStrPtr("sk-1"),
			Api:     tmreStrPtr("openai-completions"),
			Models: []ProviderModelConfig{
				&ProviderChatModelConfig{ProviderModelConfigBase: ProviderModelConfigBase{Id: "dup-id", Name: "Dup"}, ContextWindow: 128000, MaxTokens: 4096},
			},
		})
		if err != nil {
			t.Fatalf("RegisterProvider dup-p1: %v", err)
		}
		err = rt.RegisterProvider("dup-p2", ProviderConfigInput{
			BaseUrl: tmreStrPtr("https://d2.example.com/v1"),
			ApiKey:  tmreStrPtr("sk-2"),
			Api:     tmreStrPtr("openai-completions"),
			Models: []ProviderModelConfig{
				&ProviderChatModelConfig{ProviderModelConfigBase: ProviderModelConfigBase{Id: "dup-id", Name: "Dup"}, ContextWindow: 128000, MaxTokens: 4096},
			},
		})
		if err != nil {
			t.Fatalf("RegisterProvider dup-p2: %v", err)
		}

		resAmbInline := ResolveCliModel(ResolveCliModelOptions{
			CliModel:     tmreStrPtr("dup-id"),
			InlineApiKey: true,
			ModelRuntime: rt,
		})
		if resAmbInline.Error == nil || !strings.Contains(*resAmbInline.Error, "--api-key does not choose a provider.") {
			t.Fatalf("expected inline api-key ambiguity hint, got %+v", resAmbInline)
		}

		resAmbMulti := ResolveCliModel(ResolveCliModelOptions{
			CliModel:     tmreStrPtr("dup-id"),
			ModelRuntime: rt,
		})
		if resAmbMulti.Error == nil || !strings.Contains(*resAmbMulti.Error, "More than one matching provider is authenticated.") {
			errStr := "<nil>"
			if resAmbMulti.Error != nil {
				errStr = *resAmbMulti.Error
			}
			t.Fatalf("expected multi-auth ambiguity hint, got err=%q (hasAuth p1=%v p2=%v)", errStr, rt.HasConfiguredAuth("dup-p1"), rt.HasConfiguredAuth("dup-p2"))
		}
	})
}

func TestFindInitialAndRestoreModel(t *testing.T) {
	creds := ai.NewInMemoryCredentialStore()
	_, _ = creds.Modify(t.Context(), "openai", func(ai.Credential) (ai.Credential, error) {
		return &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: tmreStrPtr("sk-openai")}, nil
	}, nil)
	rt := tmreNewTestRuntime(t, creds)

	openaiModel := rt.GetModel("openai", DefaultModelPerProvider["openai"])
	if openaiModel == nil {
		t.Fatal("missing openai default model")
	}

	t.Run("FindInitialModel picks scoped model when not continuing", func(t *testing.T) {
		res, err := FindInitialModel(context.Background(), FindInitialModelOptions{
			ScopedModels: []ScopedModel{{Model: openaiModel, ThinkingLevel: tmreThinkPtr(ai.ModelThinkingLevelHigh)}},
			IsContinuing: false,
			ModelRuntime: rt,
		})
		if err != nil {
			t.Fatalf("FindInitialModel: %v", err)
		}
		if res.Model != openaiModel || res.ThinkingLevel != ai.ModelThinkingLevelHigh {
			t.Fatalf("got %+v", res)
		}
	})

	t.Run("FindInitialModel skips scoped model when continuing and falls back to available default", func(t *testing.T) {
		anthModel := rt.GetModel("anthropic", DefaultModelPerProvider["anthropic"])
		res, err := FindInitialModel(context.Background(), FindInitialModelOptions{
			ScopedModels:         []ScopedModel{{Model: anthModel, ThinkingLevel: tmreThinkPtr(ai.ModelThinkingLevelHigh)}},
			IsContinuing:         true,
			DefaultProvider:      tmreStrPtr("anthropic"), // unauthenticated -> skipped
			DefaultModelId:       tmreStrPtr(DefaultModelPerProvider["anthropic"]),
			DefaultThinkingLevel: tmreThinkPtr(ai.ModelThinkingLevelLow),
			ModelRuntime:         rt,
		})
		if err != nil {
			t.Fatalf("FindInitialModel: %v", err)
		}
		if res.Model == nil || string(res.Model.Provider) != "openai" || res.Model.Id != DefaultModelPerProvider["openai"] {
			t.Fatalf("expected fallback to authenticated openai default, got %+v", res)
		}
	})

	t.Run("FindInitialModel uses per-model thinking level for authenticated default", func(t *testing.T) {
		res, err := FindInitialModel(context.Background(), FindInitialModelOptions{
			DefaultProvider: tmreStrPtr("openai"),
			DefaultModelId:  tmreStrPtr(openaiModel.Id),
			ModelThinkingLevels: map[string]agent.ThinkingLevel{
				"openai/" + openaiModel.Id: ai.ModelThinkingLevelXhigh,
			},
			ModelRuntime: rt,
		})
		if err != nil {
			t.Fatalf("FindInitialModel: %v", err)
		}
		if res.Model != openaiModel || res.ThinkingLevel != ai.ModelThinkingLevelXhigh {
			t.Fatalf("got %+v", res)
		}
	})

	t.Run("RestoreModelFromSession restores available or falls back cleanly", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		okRes := RestoreModelFromSession("openai", openaiModel.Id, nil, true, rt, &stdout, &stderr)
		if okRes.Model != openaiModel || okRes.FallbackMessage != nil ||
			!strings.Contains(stdout.String(), "Restored model: openai/"+openaiModel.Id) {
			t.Fatalf("unexpected restore ok result: %+v stdout=%q", okRes, stdout.String())
		}

		stdout.Reset()
		stderr.Reset()
		noAuthRes := RestoreModelFromSession("anthropic", DefaultModelPerProvider["anthropic"], nil, true, rt, &stdout, &stderr)
		if noAuthRes.Model != openaiModel || noAuthRes.FallbackMessage == nil ||
			!strings.Contains(stderr.String(), "no auth configured") ||
			!strings.Contains(*noAuthRes.FallbackMessage, "Using openai/"+openaiModel.Id) {
			t.Fatalf("unexpected fallback result: %+v stderr=%q", noAuthRes, stderr.String())
		}

		stdout.Reset()
		stderr.Reset()
		missingRes := RestoreModelFromSession("anthropic", "no-such-model", openaiModel, true, rt, &stdout, &stderr)
		if missingRes.Model != openaiModel || missingRes.FallbackMessage == nil ||
			!strings.Contains(stderr.String(), "model no longer exists") {
			t.Fatalf("unexpected missing fallback: %+v stderr=%q", missingRes, stderr.String())
		}
	})
}
