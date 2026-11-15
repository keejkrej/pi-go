// Ported from packages/coding-agent/test/model-registry.test.ts (pi v1.5.2).

package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/compat"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

func tmrgStrPtr(s string) *string { return &s }
func tmrgBoolPtr(b bool) *bool    { return &b }

func tmrgClearEnv(t *testing.T) {
	t.Helper()
	t.Setenv("PI_OFFLINE", "1")
	for _, k := range []string{
		"ANTHROPIC_API_KEY",
		"ANTHROPIC_OAUTH_TOKEN",
		"ANTHROPIC_AUTH_TOKEN",
		"OPENAI_API_KEY",
		"OPENROUTER_API_KEY",
		"CLOUDFLARE_API_KEY",
		"CLOUDFLARE_ACCOUNT_ID",
		"CLOUDFLARE_GATEWAY_ID",
		"GOOGLE_API_KEY",
		"GEMINI_API_KEY",
		"GITHUB_TOKEN",
		"COPILOT_GITHUB_TOKEN",
		"GH_TOKEN",
		"AI_GATEWAY_API_KEY",
	} {
		t.Setenv(k, "")
	}
}

func tmrgNewRegistry(t *testing.T, creds ai.CredentialStore, modelsJsonPath string) *ModelRegistry {
	t.Helper()
	tmrgClearEnv(t)
	if creds == nil {
		creds = ai.NewInMemoryCredentialStore()
	}
	storePath := filepath.Join(filepath.Dir(modelsJsonPath), "models-store.json")
	rt, err := CreateModelRuntime(t.Context(), &CreateModelRuntimeOptions{
		Credentials:     creds,
		ModelsPath:      &modelsJsonPath,
		ModelsStorePath: &storePath,
		RefreshOnCreate: tmrgBoolPtr(true),
	})
	if err != nil {
		t.Fatalf("CreateModelRuntime: %v", err)
	}
	t.Cleanup(rt.Close)
	return NewModelRegistry(rt)
}

func tmrgWriteModelsJson(t *testing.T, path string, providersObj *jsonx.Object) {
	t.Helper()
	root := jsonx.ObjectOf("providers", providersObj)
	raw, err := jsonx.MarshalIndent(root, "  ")
	if err != nil {
		t.Fatalf("jsonx.MarshalIndent: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

func tmrgProviderWithKey(apiKey string) *jsonx.Object {
	return jsonx.ObjectOf(
		"baseUrl", "https://api.example.com/v1",
		"apiKey", apiKey,
		"api", "openai-completions",
		"models", []any{
			jsonx.ObjectOf(
				"id", "custom-model",
				"name", "Custom Model",
				"reasoning", false,
				"input", []any{"text"},
				"cost", jsonx.ObjectOf("input", 0, "output", 0, "cacheRead", 0, "cacheWrite", 0),
				"contextWindow", 100000,
				"maxTokens", 8000,
			),
		},
	)
}

func tmrgModelsForProvider(reg *ModelRegistry, provider string) []*ai.Model {
	var out []*ai.Model
	for _, m := range reg.GetAll() {
		if m != nil && string(m.Provider) == provider {
			out = append(out, m)
		}
	}
	return out
}

func TestModelRegistryModelsJsonAndOverrides(t *testing.T) {
	t.Run("custom model replacement and provider baseUrl inheritance", func(t *testing.T) {
		dir := t.TempDir()
		modelsPath := filepath.Join(dir, "models.json")
		tmrgWriteModelsJson(t, modelsPath, jsonx.ObjectOf(
			"openrouter", jsonx.ObjectOf(
				"baseUrl", "https://my-proxy.example.com/v1",
				"api", "openai-completions",
				"models", []any{
					jsonx.ObjectOf("id", "anthropic/claude-sonnet-4.5"),
				},
			),
		))

		reg := tmrgNewRegistry(t, nil, modelsPath)
		models := tmrgModelsForProvider(reg, "openrouter")
		var count int
		var matched *ai.Model
		for _, m := range models {
			if m.Id == "anthropic/claude-sonnet-4.5" {
				count++
				matched = m
			}
		}
		if count != 1 || matched == nil || matched.BaseUrl != "https://my-proxy.example.com/v1" {
			t.Fatalf("expected 1 replaced model with proxy baseUrl, count=%d matched=%+v", count, matched)
		}
		// Other built-in providers unaffected.
		if len(tmrgModelsForProvider(reg, "openai")) == 0 || len(tmrgModelsForProvider(reg, "google")) == 0 {
			t.Fatal("expected other builtin providers intact")
		}
		// All models under overridden provider inherit baseUrl.
		for _, m := range models {
			if m.BaseUrl != "https://my-proxy.example.com/v1" {
				t.Fatalf("model %s did not inherit baseUrl: %q", m.Id, m.BaseUrl)
			}
		}
	})

	t.Run("compat schemas, overrides, and thinkingLevelMap", func(t *testing.T) {
		dir := t.TempDir()
		modelsPath := filepath.Join(dir, "models.json")
		tmrgWriteModelsJson(t, modelsPath, jsonx.ObjectOf(
			"demo", jsonx.ObjectOf(
				"baseUrl", "https://example.com/v1",
				"apiKey", "DEMO_KEY",
				"api", "openai-completions",
				"compat", jsonx.ObjectOf(
					"supportsUsageInStreaming", false,
					"maxTokensField", "max_tokens",
					"promptCacheKeyMode", "enabled",
					"supportsLongCacheRetention", false,
				),
				"models", []any{
					jsonx.ObjectOf(
						"id", "demo-inherited",
						"reasoning", true,
						"input", []any{"text"},
						"cost", jsonx.ObjectOf("input", 0, "output", 0, "cacheRead", 0, "cacheWrite", 0),
						"contextWindow", 1000,
						"maxTokens", 100,
						"thinkingLevelMap", jsonx.ObjectOf("minimal", nil, "high", "max"),
					),
					jsonx.ObjectOf(
						"id", "demo-overridden",
						"reasoning", true,
						"input", []any{"text"},
						"cost", jsonx.ObjectOf("input", 0, "output", 0, "cacheRead", 0, "cacheWrite", 0),
						"contextWindow", 1000,
						"maxTokens", 100,
						"compat", jsonx.ObjectOf(
							"supportsUsageInStreaming", true,
							"maxTokensField", "max_completion_tokens",
							"supportsStrictMode", false,
							"cacheControlFormat", "anthropic",
							"promptCacheKeyMode", "disabled",
							"thinkingFormat", "chat-template",
							"chatTemplateKwargs", jsonx.ObjectOf(
								"preserve_thinking", true,
								"thinking", jsonx.ObjectOf("$var", "thinking.enabled"),
							),
						),
					),
					jsonx.ObjectOf(
						"id", "args-model",
						"reasoning", true,
						"input", []any{"text"},
						"cost", jsonx.ObjectOf("input", 0, "output", 0, "cacheRead", 0, "cacheWrite", 0),
						"contextWindow", 1000,
						"maxTokens", 100,
						"compat", jsonx.ObjectOf(
							"thinkingFormat", "baseten",
							"chatTemplateArgs", jsonx.ObjectOf(
								"enable_thinking", jsonx.ObjectOf("$var", "thinking.enabled"),
							),
						),
					),
				},
			),
			"demo-anth", jsonx.ObjectOf(
				"baseUrl", "https://example.com",
				"apiKey", "DEMO_KEY",
				"api", "anthropic-messages",
				"compat", jsonx.ObjectOf(
					"supportsEagerToolInputStreaming", false,
					"supportsLongCacheRetention", false,
				),
				"models", []any{
					jsonx.ObjectOf(
						"id", "demo-model",
						"reasoning", true,
						"input", []any{"text"},
						"cost", jsonx.ObjectOf("input", 0, "output", 0, "cacheRead", 0, "cacheWrite", 0),
						"contextWindow", 1000,
						"maxTokens", 100,
					),
				},
			),
		))

		reg := tmrgNewRegistry(t, nil, modelsPath)
		if reg.GetError() != nil {
			t.Fatalf("unexpected GetError(): %s", *reg.GetError())
		}

		inh := reg.Find("demo", "demo-inherited")
		if inh == nil || inh.ThinkingLevelMap == nil {
			t.Fatalf("missing demo-inherited or thinkingLevelMap: %+v", inh)
		}
		if !inh.ThinkingLevelMap.Minimal.IsNull() {
			t.Fatalf("expected minimal -> null, got %+v", inh.ThinkingLevelMap.Minimal)
		}
		if highMapped, ok := inh.ThinkingLevelMap.High.Get(); !ok || highMapped != "max" {
			t.Fatalf("expected high -> max, got (%q, %v)", highMapped, ok)
		}
		inhCompat, ok := inh.Compat.V.(*ai.OpenAICompletionsCompat)
		if !ok || inhCompat.SupportsUsageInStreaming == nil || *inhCompat.SupportsUsageInStreaming {
			t.Fatalf("inherited compat mismatch: %+v", inh.Compat.V)
		}
		if inhCompat.PromptCacheKeyMode == nil || *inhCompat.PromptCacheKeyMode != "enabled" ||
			inhCompat.SupportsLongCacheRetention == nil || *inhCompat.SupportsLongCacheRetention {
			t.Fatalf("inherited promptCacheKeyMode mismatch: %+v", inhCompat)
		}

		ovr := reg.Find("demo", "demo-overridden")
		ovrCompat, ok := ovr.Compat.V.(*ai.OpenAICompletionsCompat)
		if !ok || ovrCompat.SupportsUsageInStreaming == nil || !*ovrCompat.SupportsUsageInStreaming ||
			ovrCompat.MaxTokensField == nil || *ovrCompat.MaxTokensField != "max_completion_tokens" ||
			ovrCompat.SupportsStrictMode == nil || *ovrCompat.SupportsStrictMode ||
			ovrCompat.CacheControlFormat == nil || *ovrCompat.CacheControlFormat != "anthropic" ||
			ovrCompat.PromptCacheKeyMode == nil || *ovrCompat.PromptCacheKeyMode != "disabled" ||
			ovrCompat.ThinkingFormat == nil || *ovrCompat.ThinkingFormat != "chat-template" {
			t.Fatalf("overridden compat mismatch: %+v", ovr.Compat.V)
		}
		if ovrCompat.ChatTemplateKwargs == nil {
			t.Fatal("expected ChatTemplateKwargs set")
		}
		if argsModel := reg.Find("demo", "args-model"); argsModel == nil {
			t.Fatal("missing args-model")
		} else if c, ok := argsModel.Compat.V.(*ai.OpenAICompletionsCompat); !ok || c.ChatTemplateArgs == nil {
			t.Fatalf("expected ChatTemplateArgs on args-model: %+v", argsModel.Compat.V)
		}

		anthModel := reg.Find("demo-anth", "demo-model")
		anthCompat, ok := anthModel.Compat.V.(*ai.AnthropicMessagesCompat)
		if !ok || anthCompat.SupportsEagerToolInputStreaming == nil || *anthCompat.SupportsEagerToolInputStreaming ||
			anthCompat.SupportsLongCacheRetention == nil || *anthCompat.SupportsLongCacheRetention {
			t.Fatalf("anthropic compat mismatch: %+v", anthModel.Compat.V)
		}
	})

	t.Run("modelOverrides samplingParams, promptCache, inputLimits, cost, headers, and refresh", func(t *testing.T) {
		dir := t.TempDir()
		modelsPath := filepath.Join(dir, "models.json")
		tmrgWriteModelsJson(t, modelsPath, jsonx.ObjectOf(
			"openrouter", jsonx.ObjectOf(
				"baseUrl", "https://my-proxy.example.com/v1",
				"compat", jsonx.ObjectOf("supportsFinishReason", true),
				"models", []any{
					jsonx.ObjectOf(
						"id", "custom/sampling-model",
						"samplingParams", jsonx.ObjectOf("temperature", 1, "top_p", 0.95),
						"samplingParamsByThinkingLevel", jsonx.ObjectOf(
							"low", jsonx.ObjectOf("temperature", 0.5),
							"max", jsonx.ObjectOf("temperature", 1),
						),
						"promptCache", jsonx.ObjectOf("short", 120),
					),
				},
				"modelOverrides", jsonx.ObjectOf(
					"anthropic/claude-sonnet-4.5", jsonx.ObjectOf(
						"name", "Proxied Sonnet",
						"cost", jsonx.ObjectOf("input", 99),
						"promptCache", jsonx.ObjectOf("short", 300),
						"samplingParams", jsonx.ObjectOf("top_p", 0.9),
						"samplingParamsByThinkingLevel", jsonx.ObjectOf("high", jsonx.ObjectOf("temperature", 0.8)),
						"headers", jsonx.ObjectOf("X-Custom-Model-Header", "value"),
					),
					"openai/gpt-4o", jsonx.ObjectOf(
						"compat", jsonx.ObjectOf(
							"supportsFinishReason", false,
							"openRouterRouting", jsonx.ObjectOf("order", []any{"anthropic", "together"}),
						),
					),
					"nonexistent/model-id", jsonx.ObjectOf("name", "Ignored"),
				),
			),
			"anthropic", jsonx.ObjectOf(
				"modelOverrides", jsonx.ObjectOf(
					"claude-sonnet-4-6", jsonx.ObjectOf("promptCache", jsonx.ObjectOf("long", 1800)),
				),
			),
			"vision", jsonx.ObjectOf(
				"baseUrl", "https://vision.example.com",
				"apiKey", "vis-key",
				"api", "openai-completions",
				"models", []any{
					jsonx.ObjectOf(
						"id", "vision-model",
						"input", []any{"text", "image"},
						"inputLimits", jsonx.ObjectOf(
							"maxRequestBytes", 32*1024*1024,
							"images", jsonx.ObjectOf(
								"maxPerRequest", 100,
								"resize", jsonx.ObjectOf(
									"maxWidth", 2000,
									"maxHeight", 2000,
									"maxBytes", 4.5*1024*1024,
									"jpegQuality", 80,
								),
							),
						),
					),
				},
				"modelOverrides", jsonx.ObjectOf(
					"vision-model", jsonx.ObjectOf(
						"inputLimits", jsonx.ObjectOf(
							"images", jsonx.ObjectOf(
								"resize", jsonx.ObjectOf("maxWidth", 1568, "maxBytes", 524288, "jpegQuality", 75),
							),
						),
					),
				),
			),
		))

		creds := ai.NewInMemoryCredentialStore()
		_, _ = creds.Modify(t.Context(), "openrouter", func(ai.Credential) (ai.Credential, error) {
			return &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: tmrgStrPtr("sk-or")}, nil
		}, nil)
		reg := tmrgNewRegistry(t, creds, modelsPath)
		if reg.GetError() != nil {
			t.Fatalf("unexpected error: %s", *reg.GetError())
		}

		// Nonexistent override ignored cleanly.
		if reg.Find("openrouter", "nonexistent/model-id") != nil {
			t.Fatal("nonexistent modelOverride should not create model")
		}

		custom := reg.Find("openrouter", "custom/sampling-model")
		if custom == nil || custom.SamplingParams == nil || custom.PromptCache == nil || custom.PromptCache.Short == nil || *custom.PromptCache.Short != 120 {
			t.Fatalf("custom sampling/promptCache mismatch: %+v", custom)
		}

		sonnet := reg.Find("openrouter", "anthropic/claude-sonnet-4.5")
		if sonnet == nil || sonnet.Name != "Proxied Sonnet" || sonnet.Cost.Input != 99 || sonnet.Cost.Output <= 0 {
			t.Fatalf("sonnet override mismatch: %+v", sonnet)
		}
		if sonnet.BaseUrl != "https://my-proxy.example.com/v1" {
			t.Fatalf("sonnet BaseUrl = %q", sonnet.BaseUrl)
		}
		orGpt4o := reg.Find("openrouter", "openai/gpt-4o")
		if orGpt4o == nil {
			t.Fatal("missing openrouter openai/gpt-4o")
		}
		sCompat, _ := orGpt4o.Compat.V.(*ai.OpenAICompletionsCompat)
		if sCompat == nil || sCompat.SupportsFinishReason == nil || *sCompat.SupportsFinishReason ||
			sCompat.OpenRouterRouting == nil || len(sCompat.OpenRouterRouting.Order) != 2 {
			t.Fatalf("openai/gpt-4o compat mismatch: %+v", sCompat)
		}
		auth := reg.GetApiKeyAndHeaders(t.Context(), sonnet)
		if !auth.Ok || auth.Headers == nil {
			t.Fatalf("expected auth ok with headers, got %+v", auth)
		}
		if hdr, ok := auth.Headers.Get("X-Custom-Model-Header"); !ok || hdr == nil || *hdr != "value" {
			t.Fatalf("expected X-Custom-Model-Header=value, got %v %v", hdr, ok)
		}

		// Builtin promptCache merges per tier.
		anthSonnet := reg.Find("anthropic", "claude-sonnet-4-6")
		if anthSonnet == nil || anthSonnet.PromptCache == nil || anthSonnet.PromptCache.Long == nil || *anthSonnet.PromptCache.Long != 1800 {
			t.Fatalf("expected merged Long promptCache=1800 on claude-sonnet-4-6, got %+v", anthSonnet)
		}

		// #9631 deep merge of inputLimits.images.resize.
		vis := reg.Find("vision", "vision-model")
		if vis == nil || vis.InputLimits == nil || vis.InputLimits.MaxRequestBytes == nil || *vis.InputLimits.MaxRequestBytes != 32*1024*1024 ||
			vis.InputLimits.Images == nil || vis.InputLimits.Images.MaxPerRequest == nil || *vis.InputLimits.Images.MaxPerRequest != 100 ||
			vis.InputLimits.Images.Resize == nil || *vis.InputLimits.Images.Resize.MaxWidth != 1568 || *vis.InputLimits.Images.Resize.MaxHeight != 2000 ||
			*vis.InputLimits.Images.Resize.MaxBytes != 524288 || *vis.InputLimits.Images.Resize.JpegQuality != 75 {
			t.Fatalf("vision inputLimits mismatch: %+v", vis)
		}

		// Refresh picks up updated override and removes override cleanly.
		tmrgWriteModelsJson(t, modelsPath, jsonx.ObjectOf(
			"openrouter", jsonx.ObjectOf(
				"modelOverrides", jsonx.ObjectOf(
					"anthropic/claude-sonnet-4.5", jsonx.ObjectOf("name", "Updated Sonnet"),
				),
			),
		))
		if _, err := reg.Refresh(t.Context()); err != nil {
			t.Fatalf("Refresh: %v", err)
		}
		if updated := reg.Find("openrouter", "anthropic/claude-sonnet-4.5"); updated == nil || updated.Name != "Updated Sonnet" {
			t.Fatalf("expected Updated Sonnet after Refresh, got %+v", updated)
		}
		tmrgWriteModelsJson(t, modelsPath, jsonx.ObjectOf())
		if _, err := reg.Refresh(t.Context()); err != nil {
			t.Fatalf("Refresh empty: %v", err)
		}
		if restored := reg.Find("openrouter", "anthropic/claude-sonnet-4.5"); restored == nil || restored.Name == "Updated Sonnet" {
			t.Fatalf("expected built-in name restored, got %+v", restored)
		}
	})
}

func TestModelRegistryApiKeyResolutionAndStatus(t *testing.T) {
	t.Run("env interpolation, escapes, literals, commands, and status checks", func(t *testing.T) {
		dir := t.TempDir()
		modelsPath := filepath.Join(dir, "models.json")
		counterFile := filepath.Join(dir, "cmd-count")
		statusCounterFile := filepath.Join(dir, "status-count")
		if err := os.WriteFile(counterFile, []byte("0"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(statusCounterFile, []byte("0"), 0o600); err != nil {
			t.Fatal(err)
		}

		t.Setenv("TEST_PART_A", "left")
		t.Setenv("TEST_PART_B", "right")
		t.Setenv("TEST_VAR_KEY", "env-api-key-value")
		t.Setenv("LITERAL_UPPER", "should-not-resolve-from-env")
		t.Setenv("TEST_MISSING_ENV_999", "")

		countCmd := fmt.Sprintf(`!sh -c 'c=$(cat "%s"); echo $((c + 1)) > "%s"; echo "key-value"'`, counterFile, counterFile)
		failCounterFile := filepath.Join(dir, "fail-count")
		if err := os.WriteFile(failCounterFile, []byte("0"), 0o600); err != nil {
			t.Fatal(err)
		}
		failCmd := fmt.Sprintf(`!sh -c 'c=$(cat "%s"); echo $((c + 1)) > "%s"; exit 1'`, failCounterFile, failCounterFile)
		statusCmd := fmt.Sprintf(`!sh -c 'echo 1 > "%s"; echo status-key'`, statusCounterFile)

		tmrgWriteModelsJson(t, modelsPath, jsonx.ObjectOf(
			"p-interp", tmrgProviderWithKey("${TEST_PART_A}_${TEST_PART_B}"),
			"p-esc-dollar", tmrgProviderWithKey("$$TEST_VAR_KEY"),
			"p-esc-bang", tmrgProviderWithKey("$!literal-$TEST_VAR_KEY"),
			"p-literal-env-name", tmrgProviderWithKey("LITERAL_UPPER"),
			"p-missing-env", tmrgProviderWithKey("$TEST_MISSING_ENV_999"),
			"p-pipe", tmrgProviderWithKey("!echo 'hello world' | tr ' ' '-'"),
			"p-cmd-uncached", tmrgProviderWithKey(countCmd),
			"p-cmd-fail", tmrgProviderWithKey(failCmd),
			"p-cmd-status", tmrgProviderWithKey(statusCmd),
		))

		reg := tmrgNewRegistry(t, nil, modelsPath)

		checkKey := func(provider, want string) {
			t.Helper()
			got := reg.GetApiKeyForProvider(t.Context(), provider)
			if got == nil || *got != want {
				t.Fatalf("provider %q apiKey = %v, want %q", provider, got, want)
			}
		}

		checkKey("p-interp", "left_right")
		checkKey("p-esc-dollar", "$TEST_VAR_KEY")
		checkKey("p-esc-bang", "!literal-env-api-key-value")
		checkKey("p-literal-env-name", "LITERAL_UPPER")
		checkKey("p-pipe", "hello-world")

		// Dynamic env tracking without caching.
		t.Setenv("TEST_PART_A", "newleft")
		checkKey("p-interp", "newleft_right")

		// Uncached command executes every lookup.
		checkKey("p-cmd-uncached", "key-value")
		checkKey("p-cmd-uncached", "key-value")
		checkKey("p-cmd-uncached", "key-value")
		rawCount, _ := os.ReadFile(counterFile)
		if n, _ := strconv.Atoi(strings.TrimSpace(string(rawCount))); n != 3 {
			t.Fatalf("command execution count = %d, want 3", n)
		}

		// Failed command returns nil and retries every time.
		if k1 := reg.GetApiKeyForProvider(t.Context(), "p-cmd-fail"); k1 != nil {
			t.Fatalf("expected nil on failed cmd, got %q", *k1)
		}
		if k2 := reg.GetApiKeyForProvider(t.Context(), "p-cmd-fail"); k2 != nil {
			t.Fatalf("expected nil on second failed cmd, got %q", *k2)
		}
		rawFail, _ := os.ReadFile(failCounterFile)
		if n, _ := strconv.Atoi(strings.TrimSpace(string(rawFail))); n != 2 {
			t.Fatalf("failed command retry count = %d, want 2", n)
		}

		// GetProviderAuthStatus and GetAvailable MUST NOT execute command-backed apiKey.
		stCmd := reg.GetProviderAuthStatus("p-cmd-status")
		if !stCmd.Configured || stCmd.Source == nil || *stCmd.Source != "models_json_command" {
			t.Fatalf("command auth status mismatch: %+v", stCmd)
		}
		var availHasCmd bool
		for _, m := range reg.GetAvailable() {
			if m != nil && string(m.Provider) == "p-cmd-status" {
				availHasCmd = true
				break
			}
		}
		if !availHasCmd {
			t.Fatal("expected p-cmd-status in GetAvailable()")
		}
		rawStatus, _ := os.ReadFile(statusCounterFile)
		if strings.TrimSpace(string(rawStatus)) != "0" {
			t.Fatalf("status/GetAvailable unexpectedly ran command! file=%q", string(rawStatus))
		}

		stInterp := reg.GetProviderAuthStatus("p-interp")
		if !stInterp.Configured || stInterp.Source == nil || *stInterp.Source != "environment" ||
			stInterp.Label == nil || *stInterp.Label != "TEST_PART_A, TEST_PART_B" {
			t.Fatalf("interpolated auth status mismatch: %+v", stInterp)
		}

		stLit := reg.GetProviderAuthStatus("p-literal-env-name")
		if !stLit.Configured || stLit.Source == nil || *stLit.Source != "models_json_key" {
			t.Fatalf("literal auth status mismatch: %+v", stLit)
		}

		stMissing := reg.GetProviderAuthStatus("p-missing-env")
		if stMissing.Configured {
			t.Fatalf("missing env should be unconfigured, got %+v", stMissing)
		}
	})

	t.Run("authHeader resolves dynamically and cloudflare-ai-gateway suppresses Bearer", func(t *testing.T) {
		dir := t.TempDir()
		modelsPath := filepath.Join(dir, "models.json")
		tokenFile := filepath.Join(dir, "tok")
		if err := os.WriteFile(tokenFile, []byte("tok-1"), 0o600); err != nil {
			t.Fatal(err)
		}
		tmrgWriteModelsJson(t, modelsPath, jsonx.ObjectOf(
			"tok-prov", jsonx.ObjectOf(
				"baseUrl", "https://tok.example.com/v1",
				"apiKey", fmt.Sprintf(`!sh -c 'cat "%s"'`, tokenFile),
				"authHeader", true,
				"api", "openai-completions",
				"models", []any{jsonx.ObjectOf("id", "m1")},
			),
			"cloudflare-ai-gateway", jsonx.ObjectOf(
				"headers", jsonx.ObjectOf("x-account", "$CLOUDFLARE_ACCOUNT_ID"),
			),
		))

		creds := AuthStorageInMemory()
		_, _ = creds.Modify(t.Context(), "cloudflare-ai-gateway", func(ai.Credential) (ai.Credential, error) {
			return &ai.ApiKeyCredential{
				Type: ai.AuthTypeApiKey,
				Key:  tmrgStrPtr("$CLOUDFLARE_API_KEY"),
				Env: map[string]string{
					"CLOUDFLARE_API_KEY":    "stored-cf-token",
					"CLOUDFLARE_ACCOUNT_ID": "stored-account",
					"CLOUDFLARE_GATEWAY_ID": "stored-gateway",
				},
			}, nil
		}, nil)

		reg := tmrgNewRegistry(t, creds, modelsPath)
		tokModel := reg.Find("tok-prov", "m1")
		auth1 := reg.GetApiKeyAndHeaders(t.Context(), tokModel)
		if !auth1.Ok || auth1.ApiKey == nil || *auth1.ApiKey != "tok-1" || auth1.Headers == nil {
			t.Fatalf("auth1 mismatch: %+v", auth1)
		}
		if hdr, ok := auth1.Headers.Get("Authorization"); !ok || hdr == nil || *hdr != "Bearer tok-1" {
			t.Fatalf("auth1 Authorization mismatch: %v %v", hdr, ok)
		}
		if err := os.WriteFile(tokenFile, []byte("tok-2"), 0o600); err != nil {
			t.Fatal(err)
		}
		auth2 := reg.GetApiKeyAndHeaders(t.Context(), tokModel)
		if !auth2.Ok || auth2.Headers == nil {
			t.Fatalf("auth2 mismatch: %+v", auth2)
		}
		if hdr, ok := auth2.Headers.Get("Authorization"); !ok || hdr == nil || *hdr != "Bearer tok-2" {
			t.Fatalf("auth2 updated Authorization mismatch: %v %v", hdr, ok)
		}

		// Cloudflare AI Gateway stored credential env interpolation + header suppression.
		cfModels := tmrgModelsForProvider(reg, "cloudflare-ai-gateway")
		if len(cfModels) == 0 {
			t.Fatal("missing cloudflare-ai-gateway models")
		}
		cfAuth := reg.GetApiKeyAndHeaders(t.Context(), cfModels[0])
		if !cfAuth.Ok || cfAuth.ApiKey != nil || cfAuth.Headers == nil {
			t.Fatalf("cfAuth mismatch: %+v", cfAuth)
		}
		if v, ok := cfAuth.Headers.Get("cf-aig-authorization"); !ok || v == nil || *v != "Bearer stored-cf-token" {
			t.Fatalf("cf-aig-authorization mismatch: %v %v", v, ok)
		}
		if v, ok := cfAuth.Headers.Get("x-account"); !ok || v == nil || *v != "stored-account" {
			t.Fatalf("x-account mismatch: %v %v", v, ok)
		}
		if v, ok := cfAuth.Headers.Get("Authorization"); !ok || v != nil {
			t.Fatalf("expected explicit nil Authorization suppression, got ok=%v v=%v", ok, v)
		}
		if v, ok := cfAuth.Headers.Get("x-api-key"); !ok || v != nil {
			t.Fatalf("expected explicit nil x-api-key suppression, got ok=%v v=%v", ok, v)
		}
		if cfAuth.Env["CLOUDFLARE_GATEWAY_ID"] != "stored-gateway" {
			t.Fatalf("expected stored gateway env preserved, got %+v", cfAuth.Env)
		}
	})

	t.Run("github-copilot OAuth filters GetAvailable by availableModelIds", func(t *testing.T) {
		copilotModels := providers.GetBuiltinModels("github-copilot")
		if len(copilotModels) == 0 {
			t.Fatal("expected github-copilot builtin models")
		}
		targetId := copilotModels[0].Id

		dir := t.TempDir()
		modelsPath := filepath.Join(dir, "models.json")
		creds := ai.NewInMemoryCredentialStore()
		_, _ = creds.Modify(t.Context(), "github-copilot", func(ai.Credential) (ai.Credential, error) {
			return &ai.OAuthCredential{
				Type: ai.AuthTypeOAuth,
				OAuthCredentials: ai.OAuthCredentials{
					Refresh: "gh-refresh",
					Access:  "tid=test;exp=9999999999;proxy-ep=proxy.individual.githubcopilot.com;",
					Expires: float64(time.Now().Add(time.Hour).UnixMilli()),
					Extra:   jsonx.ObjectOf("availableModelIds", []any{targetId}),
				},
			}, nil
		}, nil)

		reg := tmrgNewRegistry(t, creds, modelsPath)
		var availIds []string
		for _, m := range reg.GetAvailable() {
			if m != nil && string(m.Provider) == "github-copilot" {
				availIds = append(availIds, m.Id)
			}
		}
		if len(availIds) != 1 || availIds[0] != targetId {
			t.Fatalf("github-copilot GetAvailable = %v, want [%s]", availIds, targetId)
		}
		if !reg.IsUsingOAuth(copilotModels[0]) {
			t.Fatal("expected IsUsingOAuth true")
		}
	})
}

func TestModelRegistryDynamicProviderLifecycle(t *testing.T) {
	t.Run("GetProviderDisplayName and uppercase RegisterProvider literals", func(t *testing.T) {
		dir := t.TempDir()
		reg := tmrgNewRegistry(t, nil, filepath.Join(dir, "models.json"))

		if reg.GetProviderDisplayName("openai") != "OpenAI" {
			t.Fatalf("openai name = %q", reg.GetProviderDisplayName("openai"))
		}
		if reg.GetProviderDisplayName("github-copilot") != "GitHub Copilot" {
			t.Fatalf("github-copilot name = %q", reg.GetProviderDisplayName("github-copilot"))
		}
		if reg.GetProviderDisplayName("zai") != "Z.AI" {
			t.Fatalf("zai name = %q", reg.GetProviderDisplayName("zai"))
		}
		if reg.GetProviderDisplayName("unknown-provider") != "unknown-provider" {
			t.Fatalf("unknown name = %q", reg.GetProviderDisplayName("unknown-provider"))
		}

		t.Setenv("CUSTOM_NAME", "env-custom")
		t.Setenv("BEARER", "env-bearer")
		t.Setenv("MODEL_TOKEN", "env-model")
		hdrs := omap.NewMap[string, string]()
		hdrs.Set("Authorization", "BEARER")
		mHdrs := omap.NewMap[string, string]()
		mHdrs.Set("x-model-token", "MODEL_TOKEN")

		if err := reg.RegisterProvider("literal-provider", ProviderConfigInput{
			BaseUrl: tmrgStrPtr("https://provider.test/v1"),
			ApiKey:  tmrgStrPtr("CUSTOM_NAME"),
			Api:     tmrgStrPtr("openai-completions"),
			Headers: hdrs,
			Models: []ProviderModelConfig{
				&ProviderChatModelConfig{
					ProviderModelConfigBase: ProviderModelConfigBase{
						Id:      "demo-model",
						Name:    "demo-model",
						Headers: mHdrs,
					},
					ContextWindow: 100000,
					MaxTokens:     8000,
				},
			},
		}); err != nil {
			t.Fatalf("RegisterProvider: %v", err)
		}

		if k := reg.GetApiKeyForProvider(t.Context(), "literal-provider"); k == nil || *k != "CUSTOM_NAME" {
			t.Fatalf("expected literal CUSTOM_NAME, got %v", k)
		}
		m := reg.Find("literal-provider", "demo-model")
		auth := reg.GetApiKeyAndHeaders(t.Context(), m)
		if !auth.Ok || auth.Headers == nil {
			t.Fatalf("auth mismatch: %+v", auth)
		}
		if a, _ := auth.Headers.Get("Authorization"); a == nil || *a != "BEARER" {
			t.Fatalf("Authorization = %v", a)
		}
		if x, _ := auth.Headers.Get("x-model-token"); x == nil || *x != "MODEL_TOKEN" {
			t.Fatalf("x-model-token = %v", x)
		}
	})

	t.Run("failed RegisterProvider preserves existing state and validates rules", func(t *testing.T) {
		dir := t.TempDir()
		reg := tmrgNewRegistry(t, nil, filepath.Join(dir, "models.json"))

		err := reg.RegisterProvider("broken-provider", ProviderConfigInput{
			StreamSimple: func(context.Context, *ai.Model, *ai.TranscriptContext, *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
				return nil
			},
		})
		if err == nil || !strings.Contains(err.Error(), `Provider broken-provider: "api" is required when registering streamSimple.`) {
			t.Fatalf("expected missing api error, got %v", err)
		}

		if err := reg.RegisterProvider("demo-provider", ProviderConfigInput{
			BaseUrl: tmrgStrPtr("https://provider.test/v1"),
			ApiKey:  tmrgStrPtr("test-key"),
			Api:     tmrgStrPtr("openai-completions"),
			Models: []ProviderModelConfig{
				&ProviderChatModelConfig{
					ProviderModelConfigBase: ProviderModelConfigBase{Id: "demo-model", Name: "Demo"},
					ContextWindow:           128000,
					MaxTokens:               4096,
				},
			},
		}); err != nil {
			t.Fatalf("RegisterProvider valid: %v", err)
		}

		err = reg.RegisterProvider("demo-provider", ProviderConfigInput{
			BaseUrl: tmrgStrPtr("https://provider.test/v2"),
			ApiKey:  tmrgStrPtr("test-key"),
			Models: []ProviderModelConfig{
				&ProviderChatModelConfig{
					ProviderModelConfigBase: ProviderModelConfigBase{Id: "broken-model", Name: "Broken"},
					ContextWindow:           128000,
					MaxTokens:               4096,
				},
			},
		})
		if err == nil || !strings.Contains(err.Error(), `Provider demo-provider, model broken-model: no "api" specified.`) {
			t.Fatalf("expected missing model api error, got %v", err)
		}
		if reg.Find("demo-provider", "demo-model") == nil {
			t.Fatal("failed RegisterProvider removed existing demo-model")
		}
		if _, err := reg.Refresh(t.Context()); err != nil {
			t.Fatalf("Refresh: %v", err)
		}
		if reg.Find("demo-provider", "demo-model") == nil {
			t.Fatal("demo-model disappeared after Refresh")
		}
	})

	t.Run("UnregisterProvider, override persistence across Refresh, and virtual models", func(t *testing.T) {
		dir := t.TempDir()
		reg := tmrgNewRegistry(t, nil, filepath.Join(dir, "models.json"))

		if err := reg.RegisterProvider("anthropic", ProviderConfigInput{
			BaseUrl: tmrgStrPtr("https://proxy.test/anthropic"),
			OAuth: &ExtensionOAuthConfig{
				Name: "Custom Anthropic OAuth",
				Login: func(context.Context, compat.OAuthLoginCallbacks) (ai.OAuthCredentials, error) {
					return ai.OAuthCredentials{Access: "tok", Refresh: "ref", Expires: float64(time.Now().Add(time.Hour).UnixMilli())}, nil
				},
				RefreshToken: func(_ context.Context, c ai.OAuthCredentials) (ai.OAuthCredentials, error) { return c, nil },
				GetApiKey:    func(c ai.OAuthCredentials) string { return c.Access },
			},
		}); err != nil {
			t.Fatalf("RegisterProvider anthropic: %v", err)
		}
		if _, err := reg.Refresh(t.Context()); err != nil {
			t.Fatalf("Refresh: %v", err)
		}
		anthModels := tmrgModelsForProvider(reg, "anthropic")
		if len(anthModels) < 2 {
			t.Fatalf("expected built-in anthropic models preserved on baseUrl-only override, got %d", len(anthModels))
		}
		for _, m := range anthModels {
			if m.BaseUrl != "https://proxy.test/anthropic" {
				t.Fatalf("anthropic model %s BaseUrl = %q", m.Id, m.BaseUrl)
			}
		}

		// Subsequent models override replaces built-ins; later baseUrl override keeps custom models.
		if err := reg.RegisterProvider("anthropic", ProviderConfigInput{
			BaseUrl: tmrgStrPtr("https://custom.test/anthropic"),
			Api:     tmrgStrPtr("anthropic-messages"),
			Models: []ProviderModelConfig{
				&ProviderChatModelConfig{
					ProviderModelConfigBase: ProviderModelConfigBase{Id: "custom-claude", Name: "Custom Claude"},
					ContextWindow:           128000,
					MaxTokens:               4096,
				},
			},
		}); err != nil {
			t.Fatalf("RegisterProvider models replace: %v", err)
		}
		if err := reg.RegisterProvider("anthropic", ProviderConfigInput{
			BaseUrl: tmrgStrPtr("https://proxy2.test/anthropic"),
		}); err != nil {
			t.Fatalf("RegisterProvider baseUrl merge: %v", err)
		}
		if _, err := reg.Refresh(t.Context()); err != nil {
			t.Fatalf("Refresh: %v", err)
		}
		after := tmrgModelsForProvider(reg, "anthropic")
		if len(after) != 1 || after[0].Id != "custom-claude" || after[0].BaseUrl != "https://proxy2.test/anthropic" {
			t.Fatalf("expected custom-claude with proxy2 baseUrl, got %+v", after)
		}

		reg.UnregisterProvider("anthropic")
		if reg.GetRegisteredProviderConfig("anthropic") != nil {
			t.Fatal("expected unregistered provider config cleared")
		}
		if restored := tmrgModelsForProvider(reg, "anthropic"); len(restored) < 2 {
			t.Fatalf("expected built-in anthropic models restored after unregister, got %d", len(restored))
		}

		// Virtual model registration/unregistration.
		if err := reg.RegisterVirtualModel(VirtualModelDefinition{
			Provider:      "anthropic",
			Id:            "auto-virtual",
			Name:          "Auto Virtual",
			ContextWindow: 128000,
			MaxTokens:     4096,
			Route: func(_ context.Context, req ModelRouteRequest) (ModelRoute, error) {
				target := reg.Find("anthropic", DefaultModelPerProvider["anthropic"])
				return ModelRoute{Model: target, ThinkingLevel: req.ThinkingLevel}, nil
			},
		}); err != nil {
			t.Fatalf("RegisterVirtualModel: %v", err)
		}
		if reg.Find("anthropic", "auto-virtual") == nil {
			t.Fatal("expected auto-virtual in registry")
		}
		reg.UnregisterVirtualModel("anthropic", "auto-virtual")
		if reg.Find("anthropic", "auto-virtual") != nil {
			t.Fatal("expected auto-virtual removed")
		}
	})
}
