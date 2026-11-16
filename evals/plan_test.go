// Ported from packages/evals/test/plan.test.ts (pi v1.0.0).

package evals_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/keejkrej/pi-go/evals"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func tplanDiscoveredFixtures() []evals.DiscoveredTestEntry {
	return []evals.DiscoveredTestEntry{
		{Name: "Add model > adds the model", File: "evals/models.docs.eval.ts"},
	}
}

func TestParseDiscoveredCases(t *testing.T) {
	t.Run("derives stable case identity from ordinary Vitest names", func(t *testing.T) {
		got, err := evals.ParseDiscoveredCases(tplanDiscoveredFixtures())
		if err != nil {
			t.Fatalf("ParseDiscoveredCases failed: %v", err)
		}
		want := []evals.DiscoveredEvalCase{
			{
				FullName: "Add model > adds the model",
				EvalSet:  "Add model",
				CaseId:   "adds the model",
				File:     "evals/models.docs.eval.ts",
			},
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Fatalf("ParseDiscoveredCases mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("parses jsonx untyped array", func(t *testing.T) {
		parsed, err := jsonx.ParseString(`[{"name":"Add model > adds the model","file":"evals/models.docs.eval.ts"}]`)
		if err != nil {
			t.Fatalf("jsonx.ParseString failed: %v", err)
		}
		got, err := evals.ParseDiscoveredCases(parsed)
		if err != nil {
			t.Fatalf("ParseDiscoveredCases(jsonx) failed: %v", err)
		}
		if len(got) != 1 || got[0].CaseId != "adds the model" {
			t.Fatalf("unexpected parsed result: %#v", got)
		}
	})

	t.Run("rejects ambiguous and duplicate identities", func(t *testing.T) {
		if _, err := evals.ParseDiscoveredCases("not-an-array"); err == nil || !strings.Contains(err.Error(), "must be an array") {
			t.Fatalf("expected non-array error, got %v", err)
		}
		if _, err := evals.ParseDiscoveredCases([]evals.DiscoveredTestEntry{{Name: "adds the model", File: "model.ts"}}); err == nil || !strings.Contains(err.Error(), "<eval set> > <case>") {
			t.Fatalf("expected separator error, got %v", err)
		}
		if _, err := evals.ParseDiscoveredCases([]evals.DiscoveredTestEntry{{Name: "a > b > c", File: "model.ts"}}); err == nil || !strings.Contains(err.Error(), "<eval set> > <case>") {
			t.Fatalf("expected extra separator error, got %v", err)
		}
		dup := append(tplanDiscoveredFixtures(), tplanDiscoveredFixtures()...)
		if _, err := evals.ParseDiscoveredCases(dup); err == nil || !strings.Contains(err.Error(), "Duplicate eval case identity") {
			t.Fatalf("expected duplicate error, got %v", err)
		}
	})
}

func TestCreateTaskPlan(t *testing.T) {
	t.Run("creates one isolated task per case, variant, model, and repetition", func(t *testing.T) {
		cases, err := evals.ParseDiscoveredCases(tplanDiscoveredFixtures())
		if err != nil {
			t.Fatalf("ParseDiscoveredCases failed: %v", err)
		}
		tasks, err := evals.CreateTaskPlan(cases, "fixture/model", 2)
		if err != nil {
			t.Fatalf("CreateTaskPlan failed: %v", err)
		}
		if len(tasks) != 4 {
			t.Fatalf("expected 4 tasks, got %d", len(tasks))
		}
		type variantRun struct {
			variant   evals.DocumentationVariant
			runNumber int
		}
		got := make([]variantRun, len(tasks))
		for i, task := range tasks {
			got[i] = variantRun{variant: task.Variant, runNumber: task.RunNumber}
		}
		want := []variantRun{
			{evals.DocumentationVariantWithoutDocs, 1},
			{evals.DocumentationVariantWithDocs, 1},
			{evals.DocumentationVariantWithDocs, 2},
			{evals.DocumentationVariantWithoutDocs, 2},
		}
		if !slices.Equal(got, want) {
			t.Fatalf("unexpected task ordering: got %+v, want %+v", got, want)
		}
	})

	t.Run("rejects invalid model identities and repetitions", func(t *testing.T) {
		cases, err := evals.ParseDiscoveredCases(tplanDiscoveredFixtures())
		if err != nil {
			t.Fatalf("ParseDiscoveredCases failed: %v", err)
		}
		for _, badModel := range []string{"model", "/model", "provider/"} {
			if _, err := evals.CreateTaskPlan(cases, badModel, 1); err == nil || !strings.Contains(err.Error(), "provider and model") {
				t.Fatalf("expected invalid model error for %q, got %v", badModel, err)
			}
		}
		if _, err := evals.CreateTaskPlan(cases, "fixture/model", 0); err == nil || !strings.Contains(err.Error(), "positive integer") {
			t.Fatalf("expected invalid repetition error, got %v", err)
		}
	})
}
