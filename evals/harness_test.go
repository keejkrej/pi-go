// Ported from packages/evals/test/harness.test.ts (pi v1.0.0).

package evals_test

import (
	"os"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/evals"
)

func TestResolveModelSelection(t *testing.T) {
	t.Run("prefers an explicit harness model", func(t *testing.T) {
		got, err := evals.ResolveModelSelection(
			&evals.PiCodingAgentModelSelection{Provider: "anthropic", Id: "claude-opus-4-6"},
			map[string]string{"PI_PROVIDER": "openai-codex", "PI_MODEL": "gpt-5.6-sol"},
		)
		if err != nil {
			t.Fatalf("ResolveModelSelection failed: %v", err)
		}
		want := evals.PiCodingAgentModelSelection{Provider: "anthropic", Id: "claude-opus-4-6"}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Fatalf("mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("uses trimmed environment defaults", func(t *testing.T) {
		got, err := evals.ResolveModelSelection(nil, map[string]string{
			"PI_PROVIDER": " openai-codex ",
			"PI_MODEL":    " gpt-5.6-sol ",
		})
		if err != nil {
			t.Fatalf("ResolveModelSelection failed: %v", err)
		}
		want := evals.PiCodingAgentModelSelection{Provider: "openai-codex", Id: "gpt-5.6-sol"}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Fatalf("mismatch (-want +got):\n%s", diff)
		}
	})

	for _, env := range []map[string]string{
		{},
		{"PI_PROVIDER": "openai-codex"},
		{"PI_MODEL": "gpt-5.6-sol"},
	} {
		t.Run("rejects incomplete model selection", func(t *testing.T) {
			if _, err := evals.ResolveModelSelection(nil, env); err == nil || !strings.Contains(err.Error(), "Select a harness model explicitly") {
				t.Fatalf("expected incomplete model error, got %v", err)
			}
		})
	}
}

func TestApplyIsolatedEnvironment(t *testing.T) {
	t.Setenv("PI_EVAL_VARIANT", "with_docs")
	t.Setenv("PI_EVAL_ARTIFACT_DIR", "/tmp/artifacts")
	oldHome, hadHome := os.LookupEnv("HOME")

	restore := evals.ApplyIsolatedEnvironment("/tmp/eval-home", "/tmp/eval-agent")
	func() {
		defer restore()
		homeDir, err := os.UserHomeDir()
		if err != nil {
			t.Fatalf("os.UserHomeDir failed: %v", err)
		}
		if homeDir != "/tmp/eval-home" {
			t.Fatalf("expected home /tmp/eval-home, got %q", homeDir)
		}
		if got := os.Getenv("PI_CODING_AGENT_DIR"); got != "/tmp/eval-agent" {
			t.Fatalf("expected PI_CODING_AGENT_DIR /tmp/eval-agent, got %q", got)
		}
		if _, exists := os.LookupEnv("PI_EVAL_VARIANT"); exists {
			t.Fatalf("expected PI_EVAL_VARIANT to be removed during isolation")
		}
		if _, exists := os.LookupEnv("PI_EVAL_ARTIFACT_DIR"); exists {
			t.Fatalf("expected PI_EVAL_ARTIFACT_DIR to be removed during isolation")
		}
	}()

	if restoredHome, ok := os.LookupEnv("HOME"); ok != hadHome || restoredHome != oldHome {
		t.Fatalf("expected HOME restored to (%q, %v), got (%q, %v)", oldHome, hadHome, restoredHome, ok)
	}
	if got := os.Getenv("PI_EVAL_VARIANT"); got != "with_docs" {
		t.Fatalf("expected PI_EVAL_VARIANT restored to with_docs, got %q", got)
	}
}

func TestDocumentationVariant(t *testing.T) {
	for _, variant := range []evals.DocumentationVariant{
		evals.DocumentationVariantWithoutDocs,
		evals.DocumentationVariantWithDocs,
	} {
		t.Run("accepts "+string(variant), func(t *testing.T) {
			got, err := evals.ResolveDocumentationVariant(string(variant))
			if err != nil {
				t.Fatalf("ResolveDocumentationVariant failed: %v", err)
			}
			if got != variant {
				t.Fatalf("got %q, want %q", got, variant)
			}
		})
	}

	for _, invalid := range []string{"", "other"} {
		t.Run("rejects invalid variant "+invalid, func(t *testing.T) {
			if _, err := evals.ResolveDocumentationVariant(invalid); err == nil || !strings.Contains(err.Error(), "PI_EVAL_VARIANT") {
				t.Fatalf("expected error mentioning PI_EVAL_VARIANT, got %v", err)
			}
		})
	}

	t.Run("strips only the documentation routing section from the default Pi prompt", func(t *testing.T) {
		prompt, err := core.BuildSystemPrompt(core.BuildSystemPromptOptions{
			Cwd:           "/workspace",
			SelectedTools: append([]string(nil), evals.DocumentationEvalTools...),
		})
		if err != nil {
			t.Fatalf("BuildSystemPrompt failed: %v", err)
		}
		for _, wantSub := range []string{
			"\n<docs>\nPi documentation (read only",
			"\n<rules>\n",
			"\n<cwd>\n/workspace\n</cwd>",
			"docs/models.md",
		} {
			if !strings.Contains(prompt, wantSub) {
				t.Fatalf("expected prompt to contain %q", wantSub)
			}
		}

		stripped := evals.ExcludePiDocumentation(prompt)
		for _, wantSub := range []string{"\n<rules>\n", "\n<cwd>\n/workspace\n</cwd>"} {
			if !strings.Contains(stripped, wantSub) {
				t.Fatalf("expected stripped prompt to preserve %q", wantSub)
			}
		}
		for _, forbidden := range []string{
			"<docs>",
			"Pi documentation",
			"docs/models.md",
			utils.GetReadmePath(),
			utils.GetDocsPath(),
			utils.GetExamplesPath(),
		} {
			if strings.Contains(stripped, forbidden) {
				t.Fatalf("expected stripped prompt not to contain %q", forbidden)
			}
		}
	})

	t.Run("verifies the prompt that was sent", func(t *testing.T) {
		prompt, err := core.BuildSystemPrompt(core.BuildSystemPromptOptions{
			Cwd:           "/workspace",
			SelectedTools: append([]string(nil), evals.DocumentationEvalTools...),
		})
		if err != nil {
			t.Fatalf("BuildSystemPrompt failed: %v", err)
		}
		stripped := evals.ExcludePiDocumentation(prompt)
		expectedFalse := false

		got, err := evals.VerifySystemPrompt(stripped, "without_docs", &expectedFalse)
		if err != nil || got != stripped {
			t.Fatalf("VerifySystemPrompt(stripped) failed: %v", err)
		}
		if _, err := evals.VerifySystemPrompt(prompt, "without_docs", &expectedFalse); err == nil || !strings.Contains(err.Error(), "does not match") {
			t.Fatalf("expected mismatch error, got %v", err)
		}
	})

	t.Run("fails closed when prompt markers are missing", func(t *testing.T) {
		if _, err := evals.ExcludePiDocumentationSafe("Instructions"); err == nil || !strings.Contains(err.Error(), "no Pi documentation section") {
			t.Fatalf("expected missing docs section error, got %v", err)
		}
		if _, err := evals.ExcludePiDocumentationSafe("\n<docs>\nPi documentation\n</docs>"); err == nil || !strings.Contains(err.Error(), "no working-directory section") {
			t.Fatalf("expected missing cwd section error, got %v", err)
		}
	})

	t.Run("rejects documentation harnesses outside the container sandbox", func(t *testing.T) {
		t.Setenv("PI_EVAL_CONTAINER", "")
		if _, err := evals.CreatePiDocumentationEvalHarness[string](); err == nil || !strings.Contains(err.Error(), "isolated container sandbox") {
			t.Fatalf("expected container sandbox error, got %v", err)
		}
	})
}
