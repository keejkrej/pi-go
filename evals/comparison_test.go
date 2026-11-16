// Ported from packages/evals/test/comparison.test.ts (pi v1.0.0).

package evals_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/evals"
)

func tcompFloat(v float64) *float64 { return &v }

type tcompMetrics struct {
	totalTokens      *float64
	hasTotalTokens   bool
	toolCalls        *float64
	totalMs          *float64
	estimatedCostUsd *float64
}

func tcompScored(variant evals.DocumentationVariant, runNumber int, score float64, overrides ...tcompMetrics) evals.EvalObservation {
	s := score
	obs := evals.EvalObservation{
		EvalSet:          "tool access",
		CaseId:           "create",
		Variant:          variant,
		Model:            "fixture/model",
		RunNumber:        runNumber,
		Outcome:          evals.EvalOutcomeScored,
		Score:            &s,
		TotalTokens:      tcompFloat(100),
		ToolCalls:        tcompFloat(2),
		TotalMs:          tcompFloat(1000),
		EstimatedCostUsd: tcompFloat(0.01),
	}
	if len(overrides) > 0 {
		m := overrides[0]
		if m.hasTotalTokens {
			obs.TotalTokens = m.totalTokens
		}
		if m.toolCalls != nil {
			obs.ToolCalls = m.toolCalls
		}
		if m.totalMs != nil {
			obs.TotalMs = m.totalMs
		}
		if m.estimatedCostUsd != nil {
			obs.EstimatedCostUsd = m.estimatedCostUsd
		}
	}
	return obs
}

func tcompErrored(variant evals.DocumentationVariant, runNumber int, totalTokens *float64) evals.EvalObservation {
	return evals.EvalObservation{
		EvalSet:     "tool access",
		CaseId:      "create",
		Variant:     variant,
		Model:       "fixture/model",
		RunNumber:   runNumber,
		Outcome:     evals.EvalOutcomeErrored,
		TotalTokens: totalTokens,
	}
}

func tcompExpectedFor(runNumbers ...int) []evals.ExpectedEvalRun {
	expected := make([]evals.ExpectedEvalRun, 0, len(runNumbers)*2)
	for _, runNumber := range runNumbers {
		for _, variant := range evals.DocumentationVariants {
			expected = append(expected, evals.ExpectedEvalRun{
				EvalSet:   "tool access",
				CaseId:    "create",
				Variant:   variant,
				Model:     "fixture/model",
				RunNumber: runNumber,
			})
		}
	}
	return expected
}

func TestSummarizeEvalObservations(t *testing.T) {
	t.Run("computes paired lift and efficiency deltas", func(t *testing.T) {
		observations := []evals.EvalObservation{
			tcompScored(evals.DocumentationVariantWithoutDocs, 1, 0, tcompMetrics{hasTotalTokens: true, totalTokens: tcompFloat(100), toolCalls: tcompFloat(3), totalMs: tcompFloat(1000)}),
			tcompScored(evals.DocumentationVariantWithDocs, 1, 1, tcompMetrics{hasTotalTokens: true, totalTokens: tcompFloat(120), toolCalls: tcompFloat(2), totalMs: tcompFloat(800)}),
			tcompScored(evals.DocumentationVariantWithoutDocs, 2, 1, tcompMetrics{hasTotalTokens: true, totalTokens: tcompFloat(200)}),
			tcompScored(evals.DocumentationVariantWithDocs, 2, 1, tcompMetrics{hasTotalTokens: true, totalTokens: tcompFloat(180)}),
		}
		report := evals.SummarizeEvalObservations("digest", tcompExpectedFor(1, 2), observations)
		if len(report.Comparisons) != 1 {
			t.Fatalf("expected 1 comparison, got %d", len(report.Comparisons))
		}
		got := report.Comparisons[0]
		want := evals.EvalSetComparison{
			EvalSet:           "tool access",
			TotalPairs:        2,
			EligiblePairs:     2,
			BlockedPairs:      0,
			ControlPassRate:   tcompFloat(0.5),
			TreatmentPassRate: tcompFloat(1),
			Lift:              tcompFloat(0.5),
			Flags:             []string{"treatment-saturated", "flaky"},
			TotalTokens:       evals.PairedMetricSummary{EligiblePairs: 2, ControlMean: tcompFloat(150), TreatmentMean: tcompFloat(150), MeanDelta: tcompFloat(0)},
			ToolCalls:         evals.PairedMetricSummary{EligiblePairs: 2, ControlMean: tcompFloat(2.5), TreatmentMean: tcompFloat(2), MeanDelta: tcompFloat(-0.5)},
			TotalMs:           evals.PairedMetricSummary{EligiblePairs: 2, ControlMean: tcompFloat(1000), TreatmentMean: tcompFloat(900), MeanDelta: tcompFloat(-100)},
			EstimatedCostUsd:  evals.PairedMetricSummary{EligiblePairs: 2, ControlMean: tcompFloat(0.01), TreatmentMean: tcompFloat(0.01), MeanDelta: tcompFloat(0)},
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Fatalf("comparison mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("fails closed for incomplete and errored pairs while retaining totals", func(t *testing.T) {
		observations := []evals.EvalObservation{
			tcompScored(evals.DocumentationVariantWithoutDocs, 1, 0),
			tcompErrored(evals.DocumentationVariantWithDocs, 1, tcompFloat(120)),
			tcompScored(evals.DocumentationVariantWithoutDocs, 2, 1, tcompMetrics{hasTotalTokens: true, totalTokens: tcompFloat(200)}),
		}
		report := evals.SummarizeEvalObservations("digest", tcompExpectedFor(1, 2), observations)
		comp := report.Comparisons[0]
		if comp.TotalPairs != 2 || comp.EligiblePairs != 0 || comp.BlockedPairs != 2 || comp.Lift != nil {
			t.Fatalf("unexpected blocked comparison summary: %+v", comp)
		}
		wantBlocked := []evals.BlockedPair{
			{EvalSet: "tool access", CaseId: "create", Model: "fixture/model", RunNumber: 1, Reasons: []string{"with_docs: errored"}},
			{EvalSet: "tool access", CaseId: "create", Model: "fixture/model", RunNumber: 2, Reasons: []string{"with_docs: expected 1 observation, found 0"}},
		}
		if diff := cmp.Diff(wantBlocked, report.BlockedPairs); diff != "" {
			t.Fatalf("blocked pairs mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(evals.OperationalMetricTotal{AvailableRuns: 2, Total: tcompFloat(300)}, report.OperationalTotals[0].TotalTokens); diff != "" {
			t.Fatalf("operational totals mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("blocks duplicate observations and keeps missing metrics distinct from zero", func(t *testing.T) {
		withoutTokens := tcompScored(evals.DocumentationVariantWithoutDocs, 1, 1, tcompMetrics{hasTotalTokens: true, totalTokens: nil})
		report := evals.SummarizeEvalObservations("digest", tcompExpectedFor(1), []evals.EvalObservation{
			withoutTokens,
			withoutTokens,
			tcompScored(evals.DocumentationVariantWithDocs, 1, 1, tcompMetrics{hasTotalTokens: true, totalTokens: tcompFloat(0)}),
		})
		if diff := cmp.Diff([]string{"without_docs: expected 1 observation, found 2"}, report.BlockedPairs[0].Reasons); diff != "" {
			t.Fatalf("duplicate blocked reasons mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(evals.OperationalMetricTotal{AvailableRuns: 0, Total: nil}, report.OperationalTotals[0].TotalTokens); diff != "" {
			t.Fatalf("missing metric mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(evals.OperationalMetricTotal{AvailableRuns: 1, Total: tcompFloat(0)}, report.OperationalTotals[1].TotalTokens); diff != "" {
			t.Fatalf("zero metric mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("formats blocked comparisons and operational totals", func(t *testing.T) {
		report := evals.SummarizeEvalObservations("digest", tcompExpectedFor(1, 2), []evals.EvalObservation{
			tcompScored(evals.DocumentationVariantWithoutDocs, 1, 1),
			tcompScored(evals.DocumentationVariantWithDocs, 1, 1),
		})
		formatted := utils.StripAnsi(evals.FormatEvalComparisonReport(report))
		for _, expectedSub := range []string{
			"Documentation Eval Comparisons",
			"Pass rate  withheld because pairs are blocked",
			"without_docs: 1 runs",
		} {
			if !strings.Contains(formatted, expectedSub) {
				t.Fatalf("expected formatted report to contain %q, got:\n%s", expectedSub, formatted)
			}
		}
	})
}
