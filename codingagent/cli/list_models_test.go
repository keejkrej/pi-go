// Ported from packages/coding-agent/test/list-models.test.ts (pi v1.5.2).

package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
)

func tlmStrPtr(s string) *string { return &s }
func tlmBoolPtr(b bool) *bool    { return &b }

func tlmNewRuntime(t *testing.T) (*core.ModelRuntime, ai.CredentialStore) {
	t.Helper()
	t.Setenv("PI_OFFLINE", "1")
	for _, k := range []string{
		"ANTHROPIC_API_KEY", "ANTHROPIC_OAUTH_TOKEN", "ANTHROPIC_AUTH_TOKEN",
		"OPENAI_API_KEY", "OPENROUTER_API_KEY", "GOOGLE_API_KEY", "GEMINI_API_KEY",
	} {
		t.Setenv(k, "")
	}
	creds := ai.NewInMemoryCredentialStore()
	rt, err := core.CreateModelRuntime(t.Context(), &core.CreateModelRuntimeOptions{
		Credentials:       creds,
		ModelsPath:        tlmStrPtr(""),
		ModelsStore:       core.NewInMemoryCodingAgentModelsStore(),
		AllowModelNetwork: false,
		RefreshOnCreate:   tlmBoolPtr(false),
	})
	if err != nil {
		t.Fatalf("CreateModelRuntime: %v", err)
	}
	t.Cleanup(rt.Close)
	return rt, creds
}

func TestListModelsEmptyTableAndFilter(t *testing.T) {
	rt, creds := tlmNewRuntime(t)

	// No configured credentials -> helpful no-models message.
	var outBuf, errBuf bytes.Buffer
	if err := lmListModelsTo(t.Context(), &outBuf, &errBuf, rt, nil); err != nil {
		t.Fatalf("lmListModelsTo(empty): %v", err)
	}
	if !strings.Contains(outBuf.String(), "No models available") && len(strings.TrimSpace(outBuf.String())) == 0 {
		t.Fatalf("unexpected empty output: %q", outBuf.String())
	}

	// Register a native provider with two models (varying token scales & capabilities) and API key auth.
	err := rt.RegisterNativeProvider(&ai.Provider{
		Id:   "alpha",
		Name: "Alpha",
		Auth: ai.ProviderAuth{
			ApiKey: ai.EnvApiKeyAuth("Alpha", []string{"ALPHA_TEST_API_KEY"}),
		},
		GetModels: func() []*ai.Model {
			return []*ai.Model{
				{
					BaseModel: ai.BaseModel{
						Id:       "m-big",
						Name:     "Big",
						Api:      "openai-completions",
						Provider: "alpha",
						BaseUrl:  "https://api.example.com/v1",
						Input:    []ai.ModelModality{ai.ModelModalityText, ai.ModelModalityImage},
					},
					Reasoning:     true,
					ContextWindow: 1_500_000,
					MaxTokens:     128_000,
				},
				{
					BaseModel: ai.BaseModel{
						Id:       "m-small",
						Name:     "Small",
						Api:      "openai-completions",
						Provider: "alpha",
						BaseUrl:  "https://api.example.com/v1",
						Input:    []ai.ModelModality{ai.ModelModalityText},
					},
					Reasoning:     false,
					ContextWindow: 1_000_000,
					MaxTokens:     512,
				},
			}
		},
		Stream: func(context.Context, *ai.Model, *ai.TranscriptContext, ai.ProviderStreamOptions) *ai.AssistantMessageEventStream {
			panic("not used")
		},
		StreamSimple: func(context.Context, *ai.Model, *ai.TranscriptContext, *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
			panic("not used")
		},
	})
	if err != nil {
		t.Fatalf("RegisterNativeProvider: %v", err)
	}
	_, _ = creds.Modify(t.Context(), "alpha", func(ai.Credential) (ai.Credential, error) {
		return &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: tlmStrPtr("sk-alpha")}, nil
	}, nil)

	outBuf.Reset()
	errBuf.Reset()
	if err := lmListModelsTo(t.Context(), &outBuf, &errBuf, rt, nil); err != nil {
		t.Fatalf("lmListModelsTo(populated): %v", err)
	}
	lines := strings.Split(strings.TrimSpace(outBuf.String()), "\n")
	if len(lines) < 3 {
		t.Fatalf("expected header + 2 rows, got:\n%s", outBuf.String())
	}
	if !strings.HasPrefix(lines[0], "provider") || !strings.Contains(lines[0], "max-out") {
		t.Fatalf("unexpected header line: %q", lines[0])
	}
	output := outBuf.String()
	if !strings.Contains(output, "1.5M") || !strings.Contains(output, "128K") || !strings.Contains(output, "1M") || !strings.Contains(output, "512") {
		t.Fatalf("expected formatted token counts, got:\n%s", output)
	}
	if !strings.Contains(output, "yes       yes") || !strings.Contains(output, "no        no") {
		t.Fatalf("expected thinking/images yes/no columns, got:\n%s", output)
	}

	// Search filter matching one model.
	outBuf.Reset()
	if err := lmListModelsTo(t.Context(), &outBuf, &errBuf, rt, tlmStrPtr("m-big")); err != nil {
		t.Fatalf("lmListModelsTo(filter): %v", err)
	}
	if !strings.Contains(outBuf.String(), "m-big") || strings.Contains(outBuf.String(), "m-small") {
		t.Fatalf("filter mismatch:\n%s", outBuf.String())
	}

	// Non-matching search pattern.
	outBuf.Reset()
	if err := lmListModelsTo(t.Context(), &outBuf, &errBuf, rt, tlmStrPtr("zzzz-no-match")); err != nil {
		t.Fatalf("lmListModelsTo(no match): %v", err)
	}
	if strings.TrimSpace(outBuf.String()) != `No models matching "zzzz-no-match"` {
		t.Fatalf("unexpected non-matching output: %q", outBuf.String())
	}
}
