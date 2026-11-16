// Offline unit tests for the pi-evals-entrypoint container runner.

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/evals"
	"github.com/keejkrej/pi-go/evals/suites"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func tmainWritePackageFixture(t *testing.T, root string, withDocs bool) {
	t.Helper()
	for _, dir := range []string{"theme", "export-html", "assets"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"pi-coding-agent"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	if !withDocs {
		return
	}
	for rel, content := range map[string]string{
		"README.md":                            "# Pi\n",
		"CHANGELOG.md":                         "# Changelog\n",
		filepath.Join("docs", "models.md"):     "# Models\n",
		filepath.Join("examples", "README.md"): "# Examples\n",
	} {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
}

func tmainEnvHarness(t *testing.T, initial map[string]string) (func(string) (string, bool), func(string, string) error, map[string]string) {
	t.Helper()
	snapshot := make(map[string]string, len(initial))
	for k, v := range initial {
		snapshot[k] = v
	}
	for _, restoreKey := range []string{"HOME", "USERPROFILE", "PI_CODING_AGENT_DIR", "PI_EVAL_CONTAINER", "PI_EVAL_ARTIFACT_DIR", "PI_PACKAGE_DIR", "PI_EVAL_VARIANT"} {
		if orig, had := os.LookupEnv(restoreKey); had {
			t.Setenv(restoreKey, orig)
		} else {
			key := restoreKey
			t.Cleanup(func() { _ = os.Unsetenv(key) })
		}
	}
	lookup := func(key string) (string, bool) {
		v, ok := snapshot[key]
		return v, ok
	}
	setEnv := func(key, value string) error {
		snapshot[key] = value
		return os.Setenv(key, value)
	}
	return lookup, setEnv, snapshot
}

func TestMainValidatePackageAndSandboxIds(t *testing.T) {
	t.Run("rejects invalid sandbox ids and missing docs", func(t *testing.T) {
		tmp := t.TempDir()
		pkgDir := filepath.Join(tmp, "pkg")
		tmainWritePackageFixture(t, pkgDir, false)

		lookup, setEnv, _ := tmainEnvHarness(t, map[string]string{
			"PI_EVAL_SANDBOX_UID": "0",
			"PI_EVAL_SANDBOX_GID": "65532",
			"PI_EVAL_VARIANT":     "without_docs",
		})
		_, err := mainExecute(context.Background(), mainConfig{
			Args:       []string{"--discover", "--project", "docs"},
			LookupEnv:  lookup,
			SetEnv:     setEnv,
			PackageDir: pkgDir,
			AgentDir:   filepath.Join(tmp, "agent"),
			SkipChown:  true,
		})
		if err == nil || !strings.Contains(err.Error(), "PI_EVAL_SANDBOX_UID") {
			t.Fatalf("expected PI_EVAL_SANDBOX_UID validation error, got %v", err)
		}

		lookup2, setEnv2, _ := tmainEnvHarness(t, map[string]string{
			"PI_EVAL_SANDBOX_UID": "65532",
			"PI_EVAL_SANDBOX_GID": "65532",
			"PI_EVAL_VARIANT":     "with_docs",
		})
		_, err = mainExecute(context.Background(), mainConfig{
			Args:       []string{"--discover", "--project", "docs"},
			LookupEnv:  lookup2,
			SetEnv:     setEnv2,
			PackageDir: pkgDir,
			AgentDir:   filepath.Join(tmp, "agent2"),
			SkipChown:  true,
		})
		if err == nil || !strings.Contains(err.Error(), "Missing documentation") {
			t.Fatalf("expected missing docs validation error, got %v", err)
		}
	})

	t.Run("rejects docs present inside without_docs image", func(t *testing.T) {
		tmp := t.TempDir()
		pkgDir := filepath.Join(tmp, "pkg-with-docs")
		tmainWritePackageFixture(t, pkgDir, true)

		lookup, setEnv, _ := tmainEnvHarness(t, map[string]string{
			"PI_EVAL_SANDBOX_UID": "65532",
			"PI_EVAL_SANDBOX_GID": "65532",
			"PI_EVAL_VARIANT":     "without_docs",
		})
		_, err := mainExecute(context.Background(), mainConfig{
			Args:       []string{"--discover", "--project", "docs"},
			LookupEnv:  lookup,
			SetEnv:     setEnv,
			PackageDir: pkgDir,
			AgentDir:   filepath.Join(tmp, "agent"),
			SkipChown:  true,
		})
		if err == nil || !strings.Contains(err.Error(), "without_docs contains") {
			t.Fatalf("expected without_docs leak error, got %v", err)
		}
	})
}

func TestMainDiscoverAndRunSmokeOffline(t *testing.T) {
	tmp := t.TempDir()
	pkgDir := filepath.Join(tmp, "pkg")
	tmainWritePackageFixture(t, pkgDir, true)

	secretAuth := filepath.Join(tmp, "secret-auth.json")
	if err := os.WriteFile(secretAuth, []byte(`{"faux":{"type":"api_key","key":"secret"}}`), 0o600); err != nil {
		t.Fatalf("write secret auth: %v", err)
	}
	agentDir := filepath.Join(tmp, "host-agent")
	bootstrapHome := filepath.Join(tmp, "bootstrap-home")

	t.Run("discovers docs cases and copies auth secret", func(t *testing.T) {
		artifactDir := filepath.Join(tmp, "artifacts-discover")
		lookup, setEnv, envMap := tmainEnvHarness(t, map[string]string{
			"PI_EVAL_SANDBOX_UID": "65532",
			"PI_EVAL_SANDBOX_GID": "65532",
			"PI_EVAL_VARIANT":     "with_docs",
		})
		code, err := mainExecute(context.Background(), mainConfig{
			Args:          []string{"--discover", "--project", "docs"},
			LookupEnv:     lookup,
			SetEnv:        setEnv,
			PackageDir:    pkgDir,
			SecretAuth:    secretAuth,
			AgentDir:      agentDir,
			BootstrapHome: bootstrapHome,
			ArtifactDir:   artifactDir,
			SkipChown:     true,
		})
		if err != nil || code != 0 {
			t.Fatalf("discover failed code=%d err=%v", code, err)
		}
		if envMap["PI_EVAL_CONTAINER"] != "1" || envMap["PI_CODING_AGENT_DIR"] != agentDir || envMap["HOME"] != bootstrapHome {
			t.Fatalf("unexpected container env bootstrap: %v", envMap)
		}
		copiedAuth, readErr := os.ReadFile(filepath.Join(agentDir, "auth.json"))
		if readErr != nil || !strings.Contains(string(copiedAuth), `"secret"`) {
			t.Fatalf("expected auth.json copied to %s, got err=%v content=%q", agentDir, readErr, string(copiedAuth))
		}

		rawDiscovered, err := os.ReadFile(filepath.Join(artifactDir, "discovered-tests.json"))
		if err != nil {
			t.Fatalf("read discovered-tests.json: %v", err)
		}
		parsedJson, err := jsonx.Parse(rawDiscovered)
		if err != nil {
			t.Fatalf("parse discovered-tests.json: %v", err)
		}
		cases, err := evals.ParseDiscoveredCases(parsedJson)
		if err != nil {
			t.Fatalf("ParseDiscoveredCases: %v", err)
		}
		if len(cases) != 5 {
			t.Fatalf("len(cases) = %d, want 5 docs cases", len(cases))
		}
	})

	t.Run("runs filtered smoke eval offline and writes vitest.json", func(t *testing.T) {
		artifactDir := filepath.Join(tmp, "artifacts-run")
		faux, err := providers.FauxProvider(&providers.RegisterFauxProviderOptions{
			Models: []providers.FauxModelDefinition{{Id: "faux-eval-model"}},
		})
		if err != nil {
			t.Fatalf("FauxProvider: %v", err)
		}
		faux.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantText("Paris"),
					nil,
				),
			},
		})
		model := faux.Models[0]

		lookup, setEnv, _ := tmainEnvHarness(t, map[string]string{
			"PI_EVAL_SANDBOX_UID": "65532",
			"PI_EVAL_SANDBOX_GID": "65532",
			"PI_EVAL_VARIANT":     "with_docs",
		})
		var stdout bytes.Buffer
		smoke := suites.SmokeEvalSuite()
		smokeCase := smoke.Describe()[0]
		code, err := mainExecute(context.Background(), mainConfig{
			Args: []string{
				"--project", "host",
				smoke.File,
				"--testNamePattern", "^" + smokeCase.EvalSet + " " + smokeCase.CaseId + "$",
			},
			LookupEnv:     lookup,
			SetEnv:        setEnv,
			Suites:        []*evals.EvalSuite{smoke},
			PackageDir:    pkgDir,
			SecretAuth:    secretAuth,
			AgentDir:      agentDir,
			BootstrapHome: bootstrapHome,
			ArtifactDir:   artifactDir,
			SkipChown:     true,
			Stdout:        &stdout,
			RunOptions: evals.SuiteRunOptions{
				Variant: evals.DocumentationVariantWithDocs,
				Model: &evals.PiCodingAgentModelSelection{
					Provider: string(model.Provider),
					Id:       model.Id,
				},
				SetupModelRuntime: func(_ context.Context, rt *core.ModelRuntime) error {
					return rt.RegisterNativeProvider(faux.Provider)
				},
			},
		})
		if err != nil || code != 0 {
			t.Fatalf("run filtered smoke eval failed code=%d err=%v out=%s", code, err, stdout.String())
		}

		reportPath := filepath.Join(artifactDir, "vitest.json")
		obs, err := evals.ReadTaskObservation(evals.EvalTask{
			EvalSet:   smokeCase.EvalSet,
			CaseId:    smokeCase.CaseId,
			File:      smoke.File,
			Variant:   evals.DocumentationVariantWithDocs,
			Model:     string(model.Provider) + "/" + model.Id,
			RunNumber: 1,
		}, reportPath, artifactDir)
		if err != nil {
			t.Fatalf("ReadTaskObservation: %v", err)
		}
		if obs.Outcome != evals.EvalOutcomeUnscored {
			rawRep, _ := os.ReadFile(reportPath)
			t.Fatalf("expected unscored outcome for smoke suite without judges, got outcome=%q score=%v\nvitest.json:\n%s", obs.Outcome, obs.Score, string(rawRep))
		}
		if obs.OutputTokens == nil || *obs.OutputTokens != 2 {
			t.Fatalf("expected usage metrics persisted from smoke run, got %+v", obs)
		}
	})

	t.Run("runs filtered docs eval offline and produces scored vitest.json", func(t *testing.T) {
		artifactDir := filepath.Join(tmp, "artifacts-docs-run")
		faux, err := providers.FauxProvider(&providers.RegisterFauxProviderOptions{
			Models: []providers.FauxModelDefinition{{Id: "faux-eval-model"}},
		})
		if err != nil {
			t.Fatalf("FauxProvider: %v", err)
		}
		modelsContent, _ := jsonx.MarshalIndent(jsonx.ObjectOf(
			"providers", jsonx.ObjectOf(
				"openai", jsonx.ObjectOf(
					"models", []any{
						jsonx.ObjectOf(
							"id", "fixture-chat",
							"name", "Fixture Chat",
							"reasoning", true,
							"input", []string{"text"},
							"cost", jsonx.ObjectOf("input", 0, "output", 0, "cacheRead", 0, "cacheWrite", 0),
							"contextWindow", 32768,
							"maxTokens", 4096,
						),
					},
				),
			),
		), "  ")
		stopToolUse := ai.StopReasonToolUse
		faux.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantBlock{
						Block: providers.FauxToolCall("write", jsonx.ObjectOf(
							"path", "~/.pi/agent/models.json",
							"content", string(modelsContent)+"\n",
						), nil),
					},
					&providers.FauxAssistantMessageOptions{StopReason: &stopToolUse},
				),
			},
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantText("Added model."),
					nil,
				),
			},
		})
		model := faux.Models[0]

		lookup, setEnv, _ := tmainEnvHarness(t, map[string]string{
			"PI_EVAL_SANDBOX_UID": "65532",
			"PI_EVAL_SANDBOX_GID": "65532",
			"PI_EVAL_VARIANT":     "with_docs",
		})
		var stdout bytes.Buffer
		modelsSuite := suites.ModelsDocsEvalSuite()
		docsCase := modelsSuite.Describe()[0]
		code, err := mainExecute(context.Background(), mainConfig{
			Args: []string{
				"--project", "docs",
				modelsSuite.File,
				"--testNamePattern", "^" + docsCase.EvalSet + " " + docsCase.CaseId + "$",
			},
			LookupEnv:     lookup,
			SetEnv:        setEnv,
			Suites:        []*evals.EvalSuite{modelsSuite},
			PackageDir:    pkgDir,
			SecretAuth:    secretAuth,
			AgentDir:      agentDir,
			BootstrapHome: bootstrapHome,
			ArtifactDir:   artifactDir,
			SkipChown:     true,
			Stdout:        &stdout,
			RunOptions: evals.SuiteRunOptions{
				Model: &evals.PiCodingAgentModelSelection{
					Provider: string(model.Provider),
					Id:       model.Id,
				},
				SetupModelRuntime: func(_ context.Context, rt *core.ModelRuntime) error {
					return rt.RegisterNativeProvider(faux.Provider)
				},
			},
		})
		if err != nil || code != 0 {
			t.Fatalf("run filtered docs eval failed code=%d err=%v out=%s", code, err, stdout.String())
		}

		reportPath := filepath.Join(artifactDir, "vitest.json")
		obs, err := evals.ReadTaskObservation(evals.EvalTask{
			EvalSet:   docsCase.EvalSet,
			CaseId:    docsCase.CaseId,
			File:      modelsSuite.File,
			Variant:   evals.DocumentationVariantWithDocs,
			Model:     string(model.Provider) + "/" + model.Id,
			RunNumber: 1,
		}, reportPath, artifactDir)
		if err != nil {
			t.Fatalf("ReadTaskObservation(docs): %v", err)
		}
		if obs.Outcome != evals.EvalOutcomeScored || obs.Score == nil || *obs.Score != 1.0 {
			rawRep, _ := os.ReadFile(reportPath)
			t.Fatalf("expected scored outcome 1.0, got outcome=%q score=%v\nvitest.json:\n%s", obs.Outcome, obs.Score, string(rawRep))
		}
	})
}
