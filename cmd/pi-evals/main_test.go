// Unit tests for cmd/pi-evals CLI argument parsing, discovery matching, and pipeline orchestration.

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/evals"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func TestMainParseEvalCli(t *testing.T) {
	t.Run("parses CLI flags and file selectors", func(t *testing.T) {
		opts, err := mainParseEvalCli([]string{
			"--provider", "openai",
			"--model=gpt-5",
			"--runs-per-variant", "2",
			"-t", "adds model",
			"suites/models_docs_eval.go",
		}, map[string]string{})
		if err != nil {
			t.Fatalf("mainParseEvalCli failed: %v", err)
		}
		if opts.Provider != "openai" || opts.Model != "gpt-5" || opts.RunsPerVariant != 2 {
			t.Fatalf("unexpected options: %+v", opts)
		}
		if len(opts.RequestedFiles) != 1 || opts.RequestedFiles[0] != "suites/models_docs_eval.go" {
			t.Fatalf("unexpected requested files: %+v", opts.RequestedFiles)
		}
		if len(opts.DiscoveryArgs) != 2 || opts.DiscoveryArgs[0] != "-t" || opts.DiscoveryArgs[1] != "adds model" {
			t.Fatalf("unexpected discovery args: %+v", opts.DiscoveryArgs)
		}
	})

	t.Run("uses env defaults and rejects partial selections", func(t *testing.T) {
		opts, err := mainParseEvalCli(nil, map[string]string{
			"PI_PROVIDER":              "anthropic",
			"PI_MODEL":                 "claude",
			"PI_EVAL_RUNS_PER_VARIANT": "3",
		})
		if err != nil {
			t.Fatalf("mainParseEvalCli failed: %v", err)
		}
		if opts.Provider != "anthropic" || opts.Model != "claude" || opts.RunsPerVariant != 3 {
			t.Fatalf("unexpected env defaults: %+v", opts)
		}
		if _, err := mainParseEvalCli(nil, map[string]string{"PI_PROVIDER": "anthropic"}); err == nil || !strings.Contains(err.Error(), "Set both PI_PROVIDER and PI_MODEL") {
			t.Fatalf("expected env mismatch error, got %v", err)
		}
		if _, err := mainParseEvalCli([]string{"--provider", "anthropic"}, nil); err == nil || !strings.Contains(err.Error(), "CLI model selection requires both") {
			t.Fatalf("expected partial CLI model error, got %v", err)
		}
		if _, err := mainParseEvalCli([]string{"--unknown"}, nil); err == nil || !strings.Contains(err.Error(), "Unsupported eval argument") {
			t.Fatalf("expected unsupported flag error, got %v", err)
		}
	})
}

func TestMainCompareDiscovery(t *testing.T) {
	left := []evals.DiscoveredEvalCase{
		{FullName: "Set > case", EvalSet: "Set", CaseId: "case", File: "/repo/evals/suites/models_docs_eval.go"},
	}
	right := []evals.DiscoveredEvalCase{
		{FullName: "Set > case", EvalSet: "Set", CaseId: "case", File: "/repo/packages/evals/suites/models_docs_eval.go"},
	}
	if err := mainCompareDiscovery(left, right); err != nil {
		t.Fatalf("expected matching normalized discovery, got %v", err)
	}
	if err := mainCompareDiscovery(left, []evals.DiscoveredEvalCase{
		{FullName: "Set > other", EvalSet: "Set", CaseId: "other", File: "/repo/evals/suites/models_docs_eval.go"},
	}); err == nil || !strings.Contains(err.Error(), "different eval cases") {
		t.Fatalf("expected mismatch error, got %v", err)
	}
}

func TestMainRunEndToEndOffline(t *testing.T) {
	pkgRoot := t.TempDir()
	suitesDir := filepath.Join(pkgRoot, "suites")
	if err := os.MkdirAll(suitesDir, 0o700); err != nil {
		t.Fatalf("MkdirAll suites failed: %v", err)
	}
	docEvalPath := filepath.Join(suitesDir, "models_docs_eval.go")
	if err := os.WriteFile(docEvalPath, []byte("package suites\n"), 0o600); err != nil {
		t.Fatalf("WriteFile doc eval failed: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := mainRun(context.Background(), []string{"--provider", "fixture", "--model", "m1"}, mainDeps{
		stdout:      &stdout,
		stderr:      &stderr,
		env:         map[string]string{},
		packageRoot: pkgRoot,
		requireAuth: func(provider string) (string, error) {
			return filepath.Join(pkgRoot, "auth.json"), nil
		},
		buildImages: func(context.Context) (evals.BuiltImages, error) {
			return evals.BuiltImages{
				WithoutDocs: evals.BuiltImage{Name: "img-wo", Id: "sha256:1"},
				WithDocs:    evals.BuiltImage{Name: "img-w", Id: "sha256:2"},
			}, nil
		},
		discover: func(_ context.Context, dctx evals.DockerContext, variant evals.DocumentationVariant, _ []string, _ []string) (string, error) {
			dir := filepath.Join(dctx.ArtifactDirectory, "discovery", string(variant))
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return "", err
			}
			out := filepath.Join(dir, "discovered-tests.json")
			payload, _ := jsonx.Marshal([]evals.DiscoveredTestEntry{
				{Name: "Add model > adds one", File: "/repo/evals/suites/models_docs_eval.go"},
			})
			if err := os.WriteFile(out, payload, 0o600); err != nil {
				return "", err
			}
			return out, nil
		},
		runTask: func(_ context.Context, dctx evals.DockerContext, task evals.EvalTask) (string, error) {
			dir := filepath.Join(dctx.ArtifactDirectory, "tasks", string(task.Variant))
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return "", err
			}
			score := 1.0
			report := evals.VitestJsonReport{
				NumPassedTests: 1,
				NumTotalTests:  1,
				Success:        true,
				TestResults: []evals.VitestTestResult{{
					Name:   task.File,
					Status: "passed",
					AssertionResults: []evals.VitestAssertionResult{{
						AncestorTitles:  []string{task.EvalSet},
						FullName:        task.EvalSet + " " + task.CaseId,
						Status:          "passed",
						Title:           task.CaseId,
						FailureMessages: []string{},
						Meta: &evals.VitestReportMeta{
							Eval: &evals.ReportCaseEval{
								AvgScore: &score,
								Scores:   []evals.ReportCaseScore{{Name: "StructuredOutputJudge", Score: &score}},
							},
							Harness: &evals.ReportCaseHarness{
								Name: string(task.Variant),
								Run: &evals.HarnessRun{
									Usage: &evals.UsageSummary{
										Provider:    "fixture",
										Model:       "m1",
										InputTokens: 10,
										TotalTokens: 15,
									},
									Errors: []evals.HarnessError{},
								},
							},
						},
					}},
				}},
			}
			reportPath := filepath.Join(dir, "vitest.json")
			data, err := jsonx.Marshal(report)
			if err != nil {
				return "", err
			}
			if err := os.WriteFile(reportPath, data, 0o600); err != nil {
				return "", err
			}
			return reportPath, nil
		},
	})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Documentation Eval Comparisons") || !strings.Contains(stdout.String(), "Pass rate  +0.0 pp") {
		t.Fatalf("unexpected CLI report output:\n%s", stdout.String())
	}
}
