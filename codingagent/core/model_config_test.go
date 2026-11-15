// Ported from packages/coding-agent/src/core/model-config.ts & config-schemas.test.ts (pi v1.5.2).

package core

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func tmcWriteModelsJson(t *testing.T, dir, content string) string {
	t.Helper()
	p := filepath.Join(dir, "models.json")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", p, err)
	}
	return p
}

func TestLoadModelConfigMissingAndEmpty(t *testing.T) {
	cfg := LoadModelConfig("")
	if cfg.GetError() != nil {
		t.Fatalf("empty path unexpected error: %s", *cfg.GetError())
	}
	if len(cfg.GetProviderIds()) != 0 {
		t.Fatalf("expected no providers, got %v", cfg.GetProviderIds())
	}

	missing := LoadModelConfig(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if missing.GetError() != nil {
		t.Fatalf("missing file unexpected error: %s", *missing.GetError())
	}
	if missing.GetProvider("local") != nil {
		t.Fatalf("expected nil for unknown provider")
	}
}

func TestLoadModelConfigValidWithCommentsBomAndDeepClone(t *testing.T) {
	dir := t.TempDir()
	path := tmcWriteModelsJson(t, dir, "\ufeff"+`// Leading comment
{
	"$schema": "https://pi.dev/schemas/models.schema.json",
	"providers": {
		// Inline provider comment with trailing comma support
		"local": {
			"name": "Local LLM",
			"baseUrl": "http://localhost:11434/v1",
			"api": "openai-completions",
			"headers": {"X-Custom": "alpha"},
			"compat": {
				"zaiToolStream": true,
				"thinkingTokenBudgetField": "thinking_budget",
				"chatTemplateKwargs": {"budget": {"$var": "thinking.budget"}},
				"supportsMidConvoSystemMessages": true
			},
			"models": [
				{
					"id": "model-a",
					"name": "Model A",
					"contextWindow": 32768,
					"maxTokens": 4096,
					"inputLimits": {"maxRequestBytes": 1024, "images": {"maxPerRequest": 2}},
					"promptCache": {"short": 300},
					"cost": {"input": 1.5, "output": 2.5, "cacheRead": 0.25, "cacheWrite": 0.5},
				},
			],
			"modelOverrides": {
				"model-a": {
					"name": "Overridden A",
					"cost": {"input": 0.5}
				}
			}
		}
	}
}`)

	cfg := LoadModelConfig(path)
	if cfg.GetError() != nil {
		t.Fatalf("LoadModelConfig unexpected error: %s", *cfg.GetError())
	}
	if ids := cfg.GetProviderIds(); !slices.Equal(ids, []string{"local"}) {
		t.Fatalf("GetProviderIds = %v, want [local]", ids)
	}

	got := cfg.GetProvider("local")
	if got == nil || got.Name == nil || *got.Name != "Local LLM" {
		t.Fatalf("GetProvider(local) = %+v", got)
	}
	if len(got.Models) != 1 || got.Models[0].Id != "model-a" {
		t.Fatalf("unexpected models: %+v", got.Models)
	}
	if got.ModelOverrides == nil {
		t.Fatalf("expected ModelOverrides map")
	}
	ov, ok := got.ModelOverrides.Get("model-a")
	if !ok || ov.Cost == nil || ov.Cost.Input == nil || *ov.Cost.Input != 0.5 {
		t.Fatalf("unexpected modelOverride: ok=%v ov=%+v", ok, ov)
	}

	// Mutating the returned provider must not affect subsequent GetProvider reads.
	mutName := "Mutated"
	got.Name = &mutName
	got.Models[0].Id = "mutated-id"
	got.Headers.Set("X-Custom", "beta")

	fresh := cfg.GetProvider("local")
	if *fresh.Name != "Local LLM" || fresh.Models[0].Id != "model-a" {
		t.Fatalf("GetProvider returned aliased state: %+v", fresh)
	}
	if hdr, _ := fresh.Headers.Get("X-Custom"); hdr != "alpha" {
		t.Fatalf("headers mutated across GetProvider calls: %q", hdr)
	}
}

func TestLoadModelConfigRejectsInvalidDocuments(t *testing.T) {
	dir := t.TempDir()

	// Empty model ID fails schema validation.
	emptyIdPath := tmcWriteModelsJson(t, dir, `{"providers":{"local":{"models":[{"id":""}]}}}`)
	cfgEmptyId := LoadModelConfig(emptyIdPath)
	if cfgEmptyId.GetError() == nil || !strings.Contains(*cfgEmptyId.GetError(), "Invalid models.json schema:") {
		t.Fatalf("expected validation error for empty model id, got %v", cfgEmptyId.GetError())
	}

	// Non-positive token limits fail on both models and modelOverrides.
	for _, field := range []string{"contextWindow", "maxTokens"} {
		for _, val := range []int{0, -1} {
			p1 := filepath.Join(dir, fmt.Sprintf("models_%s_%d.json", field, val))
			if err := os.WriteFile(p1, []byte(fmt.Sprintf(`{"providers":{"local":{"models":[{"id":"m",%q:%d}]}}}`, field, val)), 0o600); err != nil {
				t.Fatal(err)
			}
			c1 := LoadModelConfig(p1)
			if c1.GetError() == nil {
				t.Fatalf("expected validation error for models[].%s=%d", field, val)
			}

			p2 := filepath.Join(dir, fmt.Sprintf("override_%s_%d.json", field, val))
			if err := os.WriteFile(p2, []byte(fmt.Sprintf(`{"providers":{"local":{"modelOverrides":{"m":{%q:%d}}}}}`, field, val)), 0o600); err != nil {
				t.Fatal(err)
			}
			c2 := LoadModelConfig(p2)
			if c2.GetError() == nil {
				t.Fatalf("expected validation error for modelOverrides.%s=%d", field, val)
			}
		}
	}

	// Positive 1 passes.
	okPath := tmcWriteModelsJson(t, dir, `{
		"providers": {
			"local": {
				"models": [{"id": "m", "contextWindow": 1, "maxTokens": 1}],
				"modelOverrides": {"m": {"contextWindow": 1, "maxTokens": 1}}
			}
		}
	}`)
	if errPtr := LoadModelConfig(okPath).GetError(); errPtr != nil {
		t.Fatalf("expected contextWindow=1,maxTokens=1 to pass, got %s", *errPtr)
	}

	// Invalid JSON syntax sets File error.
	badJson := tmcWriteModelsJson(t, dir, `{"providers": {`)
	badCfg := LoadModelConfig(badJson)
	if badCfg.GetError() == nil || !strings.Contains(*badCfg.GetError(), "File: "+badJson) {
		t.Fatalf("expected syntax error mentioning File path, got %v", badCfg.GetError())
	}

	// Unreadable path (directory) returns read error.
	dirCfg := LoadModelConfig(dir)
	if dirCfg.GetError() == nil {
		t.Fatalf("expected error loading directory path %s", dir)
	}
}
