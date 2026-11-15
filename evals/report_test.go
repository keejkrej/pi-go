// Ported from packages/evals/test/report.test.ts (pi v1.0.0).

package evals_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/keejkrej/pi-go/evals"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func treportTask() evals.EvalTask {
	return evals.EvalTask{
		File:      "evals/example.docs.eval.ts",
		FullName:  "Example workflow > handles the case",
		EvalSet:   "Example workflow",
		CaseId:    "handles the case",
		Variant:   evals.DocumentationVariantWithoutDocs,
		Model:     "fixture/model",
		RunNumber: 1,
	}
}

const treportSession = "{\"type\":\"session\"}\n"

type treportAssertion struct {
	caseId string
	status string
	meta   map[string]any
}

func treportScoredMeta(opts struct {
	setAvgScore bool
	avgScore    *float64
	model       string
	errors      []any
	artifacts   map[string]any
}) map[string]any {
	task := treportTask()
	var avgScoreVal any = 0.5
	if opts.setAvgScore {
		if opts.avgScore == nil {
			avgScoreVal = nil
		} else {
			avgScoreVal = *opts.avgScore
		}
	}
	model := "model"
	if opts.model != "" {
		model = opts.model
	}
	errorsList := opts.errors
	if errorsList == nil {
		errorsList = []any{}
	}
	artifactsMap := opts.artifacts
	if artifactsMap == nil {
		artifactsMap = map[string]any{
			"runId":                         "run-1",
			evals.PiSessionSnapshotArtifact: treportSession,
		}
	}
	return map[string]any{
		"eval": map[string]any{
			"avgScore": avgScoreVal,
			"scores": []any{
				map[string]any{"name": "StructuredOutputJudge", "score": 0.5},
			},
			"thresholdFailed": false,
		},
		"harness": map[string]any{
			"name": string(task.Variant),
			"run": map[string]any{
				"output": map[string]any{"ok": true},
				"session": map[string]any{
					"events": []any{
						map[string]any{"type": "message", "role": "user", "content": "prompt"},
					},
				},
				"usage": map[string]any{
					"provider":     "fixture",
					"model":        model,
					"inputTokens":  10,
					"outputTokens": 5,
					"totalTokens":  15,
					"toolCalls":    1,
					"metadata": map[string]any{
						"cacheReadTokens":  2,
						"cacheWriteTokens": 3,
						"estimatedCostUsd": 0.01,
					},
				},
				"timings":   map[string]any{"totalMs": 1234},
				"artifacts": artifactsMap,
				"errors":    errorsList,
			},
		},
	}
}

func treportWriteReport(t *testing.T, assertions []treportAssertion) (string, string) {
	t.Helper()
	task := treportTask()
	dir := t.TempDir()
	reportPath := filepath.Join(dir, "vitest.json")
	results := make([]map[string]any, 0, len(assertions))
	counts := map[string]int{}
	for _, a := range assertions {
		caseId := a.caseId
		if caseId == "" {
			caseId = task.CaseId
		}
		status := a.status
		if status == "" {
			status = "passed"
		}
		meta := a.meta
		if meta == nil {
			meta = map[string]any{}
		}
		counts[status]++
		results = append(results, map[string]any{
			"ancestorTitles":  []string{task.EvalSet},
			"fullName":        task.EvalSet + " " + caseId,
			"status":          status,
			"title":           caseId,
			"failureMessages": []string{},
			"meta":            meta,
		})
	}
	suiteStatus := "passed"
	if counts["failed"] > 0 {
		suiteStatus = "failed"
	}
	payload := map[string]any{
		"numFailedTests":  counts["failed"],
		"numPassedTests":  counts["passed"],
		"numPendingTests": counts["pending"] + counts["skipped"] + counts["disabled"],
		"numTodoTests":    counts["todo"],
		"numTotalTests":   len(results),
		"startTime":       0,
		"success":         counts["failed"] == 0,
		"testResults": []map[string]any{
			{
				"message":          "",
				"name":             "/repo/packages/evals/evals/example.docs.eval.ts",
				"status":           suiteStatus,
				"assertionResults": results,
			},
		},
	}
	data, err := jsonx.Marshal(payload)
	if err != nil {
		t.Fatalf("jsonx.Marshal report failed: %v", err)
	}
	if err := os.WriteFile(reportPath, data, 0o600); err != nil {
		t.Fatalf("WriteFile report failed: %v", err)
	}
	return dir, reportPath
}

func treportFloatPtr(v float64) *float64 { return &v }

func TestReadTaskObservation(t *testing.T) {
	task := treportTask()

	for _, status := range []string{"skipped", "todo", "disabled"} {
		t.Run("preserves a skipped outcome for "+status+" cases without harness runs", func(t *testing.T) {
			dir, reportPath := treportWriteReport(t, []treportAssertion{{status: status}})
			obs, err := evals.ReadTaskObservation(task, reportPath, dir)
			if err != nil {
				t.Fatalf("ReadTaskObservation failed: %v", err)
			}
			if obs.Outcome != evals.EvalOutcomeSkipped {
				t.Fatalf("expected skipped outcome, got %q", obs.Outcome)
			}
		})
	}

	t.Run("preserves a pending outcome when no harness run exists", func(t *testing.T) {
		dir, reportPath := treportWriteReport(t, []treportAssertion{{status: "pending"}})
		obs, err := evals.ReadTaskObservation(task, reportPath, dir)
		if err != nil {
			t.Fatalf("ReadTaskObservation failed: %v", err)
		}
		if obs.Outcome != evals.EvalOutcomePending {
			t.Fatalf("expected pending outcome, got %q", obs.Outcome)
		}
	})

	t.Run("preserves metrics from a failed eval with a partial harness run", func(t *testing.T) {
		meta := treportScoredMeta(struct {
			setAvgScore bool
			avgScore    *float64
			model       string
			errors      []any
			artifacts   map[string]any
		}{})
		dir, reportPath := treportWriteReport(t, []treportAssertion{{status: "failed", meta: meta}})
		obs, err := evals.ReadTaskObservation(task, reportPath, dir)
		if err != nil {
			t.Fatalf("ReadTaskObservation failed: %v", err)
		}
		want := evals.EvalObservation{
			EvalSet:          task.EvalSet,
			CaseId:           task.CaseId,
			Variant:          task.Variant,
			Model:            task.Model,
			RunNumber:        task.RunNumber,
			Outcome:          evals.EvalOutcomeErrored,
			InputTokens:      treportFloatPtr(10),
			OutputTokens:     treportFloatPtr(5),
			CacheReadTokens:  treportFloatPtr(2),
			CacheWriteTokens: treportFloatPtr(3),
			TotalTokens:      treportFloatPtr(15),
			ToolCalls:        treportFloatPtr(1),
			TotalMs:          treportFloatPtr(1234),
			EstimatedCostUsd: treportFloatPtr(0.01),
		}
		if diff := cmp.Diff(want, obs); diff != "" {
			t.Fatalf("observation mismatch (-want +got):\n%s", diff)
		}
		entries, err := os.ReadDir(filepath.Join(dir, string(task.Variant), "sessions"))
		if err != nil || len(entries) != 1 {
			t.Fatalf("expected 1 persisted session dir, got entries=%v err=%v", entries, err)
		}
		rawSession, err := os.ReadFile(filepath.Join(dir, string(task.Variant), "sessions", entries[0].Name(), "session.jsonl"))
		if err != nil || string(rawSession) != treportSession {
			t.Fatalf("unexpected persisted session: %q err=%v", string(rawSession), err)
		}
	})

	t.Run("records an errored outcome when a passed eval has no harness run", func(t *testing.T) {
		dir, reportPath := treportWriteReport(t, []treportAssertion{{status: "passed"}})
		obs, err := evals.ReadTaskObservation(task, reportPath, dir)
		if err != nil {
			t.Fatalf("ReadTaskObservation failed: %v", err)
		}
		if obs.Outcome != evals.EvalOutcomeErrored {
			t.Fatalf("expected errored outcome, got %q", obs.Outcome)
		}
	})

	t.Run("reads a scored harness run and persists the session artifact", func(t *testing.T) {
		meta := treportScoredMeta(struct {
			setAvgScore bool
			avgScore    *float64
			model       string
			errors      []any
			artifacts   map[string]any
		}{})
		dir, reportPath := treportWriteReport(t, []treportAssertion{{meta: meta}})
		obs, err := evals.ReadTaskObservation(task, reportPath, dir)
		if err != nil {
			t.Fatalf("ReadTaskObservation failed: %v", err)
		}
		want := evals.EvalObservation{
			EvalSet:          task.EvalSet,
			CaseId:           task.CaseId,
			Variant:          task.Variant,
			Model:            task.Model,
			RunNumber:        task.RunNumber,
			Outcome:          evals.EvalOutcomeScored,
			Score:            treportFloatPtr(0.5),
			InputTokens:      treportFloatPtr(10),
			OutputTokens:     treportFloatPtr(5),
			CacheReadTokens:  treportFloatPtr(2),
			CacheWriteTokens: treportFloatPtr(3),
			TotalTokens:      treportFloatPtr(15),
			ToolCalls:        treportFloatPtr(1),
			TotalMs:          treportFloatPtr(1234),
			EstimatedCostUsd: treportFloatPtr(0.01),
		}
		if diff := cmp.Diff(want, obs); diff != "" {
			t.Fatalf("observation mismatch (-want +got):\n%s", diff)
		}
		entries, err := os.ReadDir(filepath.Join(dir, string(task.Variant), "sessions"))
		if err != nil || len(entries) != 1 {
			t.Fatalf("expected 1 persisted session dir, got entries=%v err=%v", entries, err)
		}
		rawSession, err := os.ReadFile(filepath.Join(dir, string(task.Variant), "sessions", entries[0].Name(), "session.jsonl"))
		if err != nil || string(rawSession) != treportSession {
			t.Fatalf("unexpected persisted session: %q err=%v", string(rawSession), err)
		}
	})

	t.Run("selects the requested case when a sibling case is skipped", func(t *testing.T) {
		meta := treportScoredMeta(struct {
			setAvgScore bool
			avgScore    *float64
			model       string
			errors      []any
			artifacts   map[string]any
		}{})
		dir, reportPath := treportWriteReport(t, []treportAssertion{
			{caseId: "sibling case", status: "skipped"},
			{caseId: task.CaseId, meta: meta},
		})
		obs, err := evals.ReadTaskObservation(task, reportPath, dir)
		if err != nil {
			t.Fatalf("ReadTaskObservation failed: %v", err)
		}
		if obs.Outcome != evals.EvalOutcomeScored || obs.Score == nil || *obs.Score != 0.5 || obs.TotalTokens == nil || *obs.TotalTokens != 15 {
			t.Fatalf("unexpected observation: %+v", obs)
		}
	})

	t.Run("treats a zero score as scored data and preserves score 0 in json", func(t *testing.T) {
		meta := treportScoredMeta(struct {
			setAvgScore bool
			avgScore    *float64
			model       string
			errors      []any
			artifacts   map[string]any
		}{setAvgScore: true, avgScore: treportFloatPtr(0)})
		dir, reportPath := treportWriteReport(t, []treportAssertion{{meta: meta}})
		obs, err := evals.ReadTaskObservation(task, reportPath, dir)
		if err != nil {
			t.Fatalf("ReadTaskObservation failed: %v", err)
		}
		if obs.Outcome != evals.EvalOutcomeScored || obs.Score == nil || *obs.Score != 0 {
			t.Fatalf("expected scored with 0, got %+v", obs)
		}
		raw, err := jsonx.Stringify(obs)
		if err != nil {
			t.Fatalf("jsonx.Stringify failed: %v", err)
		}
		parsed, err := jsonx.ParseString(raw)
		if err != nil {
			t.Fatalf("jsonx.ParseString failed: %v", err)
		}
		obj := parsed.(*jsonx.Object)
		if scoreVal, ok := obj.Get("score"); !ok || scoreVal != float64(0) {
			t.Fatalf("expected serialized JSON to preserve score 0, got %s", raw)
		}
	})

	t.Run("records an unscored outcome when a completed eval has no score", func(t *testing.T) {
		meta := treportScoredMeta(struct {
			setAvgScore bool
			avgScore    *float64
			model       string
			errors      []any
			artifacts   map[string]any
		}{setAvgScore: true, avgScore: nil})
		dir, reportPath := treportWriteReport(t, []treportAssertion{{meta: meta}})
		obs, err := evals.ReadTaskObservation(task, reportPath, dir)
		if err != nil {
			t.Fatalf("ReadTaskObservation failed: %v", err)
		}
		if obs.Outcome != evals.EvalOutcomeUnscored {
			t.Fatalf("expected unscored outcome, got %q", obs.Outcome)
		}
	})

	t.Run("records an errored outcome when the reported model does not match the task", func(t *testing.T) {
		meta := treportScoredMeta(struct {
			setAvgScore bool
			avgScore    *float64
			model       string
			errors      []any
			artifacts   map[string]any
		}{model: "other"})
		dir, reportPath := treportWriteReport(t, []treportAssertion{{meta: meta}})
		obs, err := evals.ReadTaskObservation(task, reportPath, dir)
		if err != nil {
			t.Fatalf("ReadTaskObservation failed: %v", err)
		}
		if obs.Outcome != evals.EvalOutcomeErrored {
			t.Fatalf("expected errored outcome, got %q", obs.Outcome)
		}
	})

	t.Run("records an errored outcome when a completed harness run contains errors", func(t *testing.T) {
		meta := treportScoredMeta(struct {
			setAvgScore bool
			avgScore    *float64
			model       string
			errors      []any
			artifacts   map[string]any
		}{errors: []any{map[string]any{"message": "boom"}}})
		dir, reportPath := treportWriteReport(t, []treportAssertion{{meta: meta}})
		obs, err := evals.ReadTaskObservation(task, reportPath, dir)
		if err != nil {
			t.Fatalf("ReadTaskObservation failed: %v", err)
		}
		if obs.Outcome != evals.EvalOutcomeErrored {
			t.Fatalf("expected errored outcome, got %q", obs.Outcome)
		}
	})

	t.Run("still scores a completed eval when the session artifact is missing", func(t *testing.T) {
		meta := treportScoredMeta(struct {
			setAvgScore bool
			avgScore    *float64
			model       string
			errors      []any
			artifacts   map[string]any
		}{artifacts: map[string]any{"runId": "run-1"}})
		dir, reportPath := treportWriteReport(t, []treportAssertion{{meta: meta}})
		obs, err := evals.ReadTaskObservation(task, reportPath, dir)
		if err != nil {
			t.Fatalf("ReadTaskObservation failed: %v", err)
		}
		if obs.Outcome != evals.EvalOutcomeScored || obs.Score == nil || *obs.Score != 0.5 {
			t.Fatalf("expected scored 0.5, got %+v", obs)
		}
	})
}
