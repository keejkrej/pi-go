// Tests for packages/evals/src/docker.ts port.

package evals_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/evals"
)

func TestRequireEvalAuthFile(t *testing.T) {
	t.Run("accepts valid auth file with requested provider", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("PI_CODING_AGENT_DIR", dir)
		authPath := filepath.Join(dir, "auth.json")
		if err := os.WriteFile(authPath, []byte(`{"anthropic":{"type":"api_key","key":"sk-test"}}`), 0o600); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}
		got, err := evals.RequireEvalAuthFile("anthropic")
		if err != nil {
			t.Fatalf("RequireEvalAuthFile failed: %v", err)
		}
		if got != authPath {
			t.Fatalf("got %q, want %q", got, authPath)
		}
	})

	t.Run("rejects missing file, invalid json, and missing provider", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("PI_CODING_AGENT_DIR", dir)
		if _, err := evals.RequireEvalAuthFile("anthropic"); err == nil || !strings.Contains(err.Error(), "does not exist") {
			t.Fatalf("expected missing file error, got %v", err)
		}
		authPath := filepath.Join(dir, "auth.json")
		if err := os.WriteFile(authPath, []byte(`{not-json`), 0o600); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}
		if _, err := evals.RequireEvalAuthFile("anthropic"); err == nil || !strings.Contains(err.Error(), "is invalid") {
			t.Fatalf("expected invalid json error, got %v", err)
		}
		if err := os.WriteFile(authPath, []byte(`{"openai":{"type":"api_key","key":"k"}}`), 0o600); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}
		if _, err := evals.RequireEvalAuthFile("anthropic"); err == nil || !strings.Contains(err.Error(), "has no credential for provider anthropic") {
			t.Fatalf("expected missing provider error, got %v", err)
		}
	})
}

func TestDockerOfflineOperations(t *testing.T) {
	t.Run("BuildImages invokes docker build and image inspect for both variants", func(t *testing.T) {
		var calls [][]string
		runner := func(_ context.Context, _ string, args []string, capture bool) (int, string, error) {
			calls = append(calls, slices.Clone(args))
			if capture && len(args) >= 2 && args[0] == "image" && args[1] == "inspect" {
				if strings.Contains(args[len(args)-1], "without-docs") {
					return 0, "sha256:control\n", nil
				}
				return 0, "sha256:treatment\n", nil
			}
			return 0, "", nil
		}
		images, err := evals.BuildImages(context.Background(), runner)
		if err != nil {
			t.Fatalf("BuildImages failed: %v", err)
		}
		if images.WithoutDocs.Id != "sha256:control" || images.WithDocs.Id != "sha256:treatment" {
			t.Fatalf("unexpected built images: %+v", images)
		}
		if len(calls) != 4 {
			t.Fatalf("expected 4 docker invocations (2 build + 2 inspect), got %d", len(calls))
		}
	})

	t.Run("DiscoverCases and RunTask pass exact container sandbox flags and exact regex", func(t *testing.T) {
		artifactDir := t.TempDir()
		authPath := filepath.Join(t.TempDir(), "auth.json")
		images := evals.BuiltImages{
			WithoutDocs: evals.BuiltImage{Name: "img-without", Id: "id-1"},
			WithDocs:    evals.BuiltImage{Name: "img-with", Id: "id-2"},
		}
		dctx := evals.CreateDockerContext(images, artifactDir, authPath, "openai", "gpt-5", 2)
		var captured [][]string
		dctx.Runner = func(_ context.Context, _ string, args []string, _ bool) (int, string, error) {
			captured = append(captured, slices.Clone(args))
			// Simulate RunTask writing vitest.json into bound artifact directory on second call.
			if len(captured) == 2 {
				for _, a := range args {
					if strings.HasPrefix(a, "type=bind,source=") && strings.HasSuffix(a, ",target=/artifacts") {
						dir := strings.TrimSuffix(strings.TrimPrefix(a, "type=bind,source="), ",target=/artifacts")
						_ = os.WriteFile(filepath.Join(dir, "vitest.json"), []byte(`{}`), 0o600)
					}
				}
			}
			return 0, "", nil
		}

		discoveredPath, err := evals.DiscoverCases(
			context.Background(),
			dctx,
			evals.DocumentationVariantWithoutDocs,
			[]string{"suites/models_docs_eval.go"},
			[]string{"-t", "adds"},
		)
		if err != nil {
			t.Fatalf("DiscoverCases failed: %v", err)
		}
		if !strings.HasSuffix(discoveredPath, filepath.Join("discovery", "without_docs", "discovered-tests.json")) {
			t.Fatalf("unexpected discovered path: %q", discoveredPath)
		}
		if !slices.Contains(captured[0], "--read-only") || !slices.Contains(captured[0], "--discover") {
			t.Fatalf("expected read-only and discover flags, got %v", captured[0])
		}

		task := evals.EvalTask{
			File:      "suites/models_docs_eval.go",
			FullName:  "Add (model) > adds [it]",
			EvalSet:   "Add (model)",
			CaseId:    "adds [it]",
			Variant:   evals.DocumentationVariantWithDocs,
			Model:     "openai/gpt-5",
			RunNumber: 1,
		}
		reportPath, err := evals.RunTask(context.Background(), dctx, task)
		if err != nil {
			t.Fatalf("RunTask failed: %v", err)
		}
		if reportPath == "" {
			t.Fatalf("expected non-empty reportPath")
		}
		patternIdx := slices.Index(captured[1], "--testNamePattern")
		if patternIdx == -1 || patternIdx+1 >= len(captured[1]) {
			t.Fatalf("missing --testNamePattern in args: %v", captured[1])
		}
		if gotPattern := captured[1][patternIdx+1]; gotPattern != `^Add \(model\) adds \[it\]$` {
			t.Fatalf("unexpected escaped pattern: %q", gotPattern)
		}
	})
}
