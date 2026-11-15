// Ported from packages/evals/src/report.ts (pi v1.0.0).

package evals

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/keejkrej/pi-go/internal/ansi"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// PiSessionSnapshotArtifact is the harness artifact key containing the serialized session JSONL.
const PiSessionSnapshotArtifact = "piSessionJsonl"

const (
	EvalOutcomeScored   = "scored"
	EvalOutcomeUnscored = "unscored"
	EvalOutcomeSkipped  = "skipped"
	EvalOutcomePending  = "pending"
	EvalOutcomeErrored  = "errored"
)

// EvalRunIdentity uniquely identifies one scheduled or observed eval run.
type EvalRunIdentity struct {
	EvalSet   string               `json:"evalSet"`
	CaseId    string               `json:"caseId"`
	Variant   DocumentationVariant `json:"variant"`
	Model     string               `json:"model"`
	RunNumber int                  `json:"runNumber"`
}

// ExpectedEvalRun is a planned eval run in the experiment design.
type ExpectedEvalRun = EvalRunIdentity

// EvalObservation records the outcome and optional operational metrics of a single eval run.
type EvalObservation struct {
	EvalSet          string               `json:"evalSet"`
	CaseId           string               `json:"caseId"`
	Variant          DocumentationVariant `json:"variant"`
	Model            string               `json:"model"`
	RunNumber        int                  `json:"runNumber"`
	InputTokens      *float64             `json:"inputTokens,omitzero"`
	OutputTokens     *float64             `json:"outputTokens,omitzero"`
	CacheReadTokens  *float64             `json:"cacheReadTokens,omitzero"`
	CacheWriteTokens *float64             `json:"cacheWriteTokens,omitzero"`
	TotalTokens      *float64             `json:"totalTokens,omitzero"`
	ToolCalls        *float64             `json:"toolCalls,omitzero"`
	TotalMs          *float64             `json:"totalMs,omitzero"`
	EstimatedCostUsd *float64             `json:"estimatedCostUsd,omitzero"`
	Outcome          string               `json:"outcome"`
	Score            *float64             `json:"score,omitzero"`
}

// PairedMetricSummary summarizes a metric across complete paired observations.
type PairedMetricSummary struct {
	EligiblePairs int      `json:"eligiblePairs"`
	ControlMean   *float64 `json:"controlMean"`
	TreatmentMean *float64 `json:"treatmentMean"`
	MeanDelta     *float64 `json:"meanDelta"`
}

// EvalSetComparison compares control (without_docs) and treatment (with_docs) for one eval set.
type EvalSetComparison struct {
	EvalSet           string              `json:"evalSet"`
	TotalPairs        int                 `json:"totalPairs"`
	EligiblePairs     int                 `json:"eligiblePairs"`
	BlockedPairs      int                 `json:"blockedPairs"`
	ControlPassRate   *float64            `json:"controlPassRate"`
	TreatmentPassRate *float64            `json:"treatmentPassRate"`
	Lift              *float64            `json:"lift"`
	Flags             []string            `json:"flags"`
	TotalTokens       PairedMetricSummary `json:"totalTokens"`
	ToolCalls         PairedMetricSummary `json:"toolCalls"`
	TotalMs           PairedMetricSummary `json:"totalMs"`
	EstimatedCostUsd  PairedMetricSummary `json:"estimatedCostUsd"`
}

// BlockedPair describes a paired comparison that could not be scored cleanly.
type BlockedPair struct {
	EvalSet   string   `json:"evalSet"`
	CaseId    string   `json:"caseId"`
	Model     string   `json:"model"`
	RunNumber int      `json:"runNumber"`
	Reasons   []string `json:"reasons"`
}

// OperationalMetricTotal sums an operational metric across all runs of a variant where measured.
type OperationalMetricTotal struct {
	AvailableRuns int      `json:"availableRuns"`
	Total         *float64 `json:"total"`
}

// VariantTotals aggregates operational totals for one documentation variant.
type VariantTotals struct {
	Variant          DocumentationVariant   `json:"variant"`
	Runs             int                    `json:"runs"`
	InputTokens      OperationalMetricTotal `json:"inputTokens"`
	OutputTokens     OperationalMetricTotal `json:"outputTokens"`
	CacheReadTokens  OperationalMetricTotal `json:"cacheReadTokens"`
	CacheWriteTokens OperationalMetricTotal `json:"cacheWriteTokens"`
	TotalTokens      OperationalMetricTotal `json:"totalTokens"`
	ToolCalls        OperationalMetricTotal `json:"toolCalls"`
	TotalMs          OperationalMetricTotal `json:"totalMs"`
	EstimatedCostUsd OperationalMetricTotal `json:"estimatedCostUsd"`
}

// EvalComparisonReport is the schemaVersion 3 summary across all eval sets.
type EvalComparisonReport struct {
	SchemaVersion     int                  `json:"schemaVersion"`
	ProtocolDigest    string               `json:"protocolDigest"`
	Control           DocumentationVariant `json:"control"`
	Treatment         DocumentationVariant `json:"treatment"`
	Comparisons       []EvalSetComparison  `json:"comparisons"`
	BlockedPairs      []BlockedPair        `json:"blockedPairs"`
	OperationalTotals []VariantTotals      `json:"operationalTotals"`
}

// TranscriptEvent represents a simplified event captured in a harness run transcript.
type TranscriptEvent struct {
	Type       string        `json:"type"`
	Role       string        `json:"role,omitzero"`
	Content    any           `json:"content,omitzero"`
	Id         string        `json:"id,omitzero"`
	Name       string        `json:"name,omitzero"`
	Arguments  *jsonx.Object `json:"arguments,omitzero"`
	ToolCallId string        `json:"toolCallId,omitzero"`
	Error      *HarnessError `json:"error,omitzero"`
}

// HarnessSession stores recorded transcript events.
type HarnessSession struct {
	Events []TranscriptEvent `json:"events"`
}

// UsageMetadata stores extended token cache and cost figures.
type UsageMetadata struct {
	CacheReadTokens  float64  `json:"cacheReadTokens"`
	CacheWriteTokens float64  `json:"cacheWriteTokens"`
	EstimatedCostUsd *float64 `json:"estimatedCostUsd,omitzero"`
}

// UsageSummary summarizes provider, model, and token/tool usage for one run.
type UsageSummary struct {
	Provider     string         `json:"provider"`
	Model        string         `json:"model"`
	InputTokens  float64        `json:"inputTokens"`
	OutputTokens float64        `json:"outputTokens"`
	TotalTokens  float64        `json:"totalTokens"`
	ToolCalls    float64        `json:"toolCalls"`
	Metadata     *UsageMetadata `json:"metadata,omitzero"`
}

// TimingsSummary records execution duration metrics.
type TimingsSummary struct {
	TotalMs float64 `json:"totalMs"`
}

// HarnessError captures a runtime error inside a harness run.
type HarnessError struct {
	Message string `json:"message"`
}

// HarnessRun is the serialized execution record produced by a harness run.
type HarnessRun struct {
	Output    any               `json:"output"`
	Session   *HarnessSession   `json:"session,omitzero"`
	Usage     *UsageSummary     `json:"usage,omitzero"`
	Timings   *TimingsSummary   `json:"timings,omitzero"`
	Metadata  *jsonx.Object     `json:"metadata,omitzero"`
	Artifacts map[string]string `json:"artifacts,omitzero"`
	Errors    []HarnessError    `json:"errors"`
}

// ReportCaseHarness attaches a HarnessRun under meta.harness.
type ReportCaseHarness struct {
	Name string      `json:"name"`
	Run  *HarnessRun `json:"run,omitzero"`
}

// ReportCaseScore records a single judge score.
type ReportCaseScore struct {
	Name     string        `json:"name"`
	Score    *float64      `json:"score"`
	Metadata *jsonx.Object `json:"metadata,omitzero"`
}

// ReportCaseEval records judge evaluation results under meta.eval.
type ReportCaseEval struct {
	AvgScore        *float64          `json:"avgScore"`
	Scores          []ReportCaseScore `json:"scores"`
	ThresholdFailed bool              `json:"thresholdFailed"`
}

// VitestReportMeta carries vitest-evals metadata inside a Vitest assertion result.
type VitestReportMeta struct {
	Eval    *ReportCaseEval    `json:"eval,omitzero"`
	Harness *ReportCaseHarness `json:"harness,omitzero"`
}

// VitestAssertionResult represents one test case inside a Vitest JSON report file.
type VitestAssertionResult struct {
	AncestorTitles  []string          `json:"ancestorTitles"`
	FullName        string            `json:"fullName"`
	Status          string            `json:"status"`
	Title           string            `json:"title"`
	FailureMessages []string          `json:"failureMessages"`
	Meta            *VitestReportMeta `json:"meta,omitzero"`
}

// VitestTestResult represents one test file suite inside a Vitest JSON report file.
type VitestTestResult struct {
	Message          string                  `json:"message"`
	Name             string                  `json:"name"`
	Status           string                  `json:"status"`
	AssertionResults []VitestAssertionResult `json:"assertionResults"`
}

// VitestJsonReport matches the Vitest JSON reporter schema consumed by ReadTaskObservation.
type VitestJsonReport struct {
	NumFailedTests  int                `json:"numFailedTests"`
	NumPassedTests  int                `json:"numPassedTests"`
	NumPendingTests int                `json:"numPendingTests"`
	NumTodoTests    int                `json:"numTodoTests"`
	NumTotalTests   int                `json:"numTotalTests"`
	StartTime       int64              `json:"startTime"`
	Success         bool               `json:"success"`
	TestResults     []VitestTestResult `json:"testResults"`
}

// ClassifyCaseStatus maps a Vitest assertion status string to an eval outcome category, if non-passing.
func ClassifyCaseStatus(status string) string {
	switch status {
	case "failed":
		return EvalOutcomeErrored
	case "skipped", "todo", "disabled":
		return EvalOutcomeSkipped
	case "pending":
		return EvalOutcomePending
	default:
		return ""
	}
}

func reportTaskIdentity(task EvalTask) EvalRunIdentity {
	return EvalRunIdentity{
		EvalSet:   task.EvalSet,
		CaseId:    task.CaseId,
		Variant:   task.Variant,
		Model:     task.Model,
		RunNumber: task.RunNumber,
	}
}

func reportObservationFromIdentity(identity EvalRunIdentity, outcome string) EvalObservation {
	return EvalObservation{
		EvalSet:   identity.EvalSet,
		CaseId:    identity.CaseId,
		Variant:   identity.Variant,
		Model:     identity.Model,
		RunNumber: identity.RunNumber,
		Outcome:   outcome,
	}
}

// ErroredObservation creates an errored EvalObservation for task without metrics or score.
func ErroredObservation(task EvalTask) EvalObservation {
	return reportObservationFromIdentity(reportTaskIdentity(task), EvalOutcomeErrored)
}

func reportOptionalMetric(obj *jsonx.Object, key string) (*float64, error) {
	if obj == nil {
		return nil, nil
	}
	raw, exists := obj.Get(key)
	if !exists {
		return nil, nil
	}
	num, ok := raw.(float64)
	if !ok || math.IsNaN(num) || math.IsInf(num, 0) || num < 0 {
		return nil, fmt.Errorf("%s must be a finite non-negative number.", key)
	}
	v := num
	return &v, nil
}

func reportValidateScore(evalObj *jsonx.Object) (*float64, error) {
	if evalObj == nil {
		return nil, nil
	}
	raw, exists := evalObj.Get("avgScore")
	if !exists || raw == nil {
		return nil, nil
	}
	num, ok := raw.(float64)
	if !ok || math.IsNaN(num) || math.IsInf(num, 0) || num < 0 || num > 1 {
		return nil, fmt.Errorf("Eval score must be between 0 and 1.")
	}
	v := num
	return &v, nil
}

func reportGetObject(obj *jsonx.Object, key string) *jsonx.Object {
	if obj == nil {
		return nil
	}
	raw, ok := obj.Get(key)
	if !ok {
		return nil
	}
	child, _ := raw.(*jsonx.Object)
	return child
}

func reportGetString(obj *jsonx.Object, key string) (string, bool) {
	if obj == nil {
		return "", false
	}
	raw, ok := obj.Get(key)
	if !ok {
		return "", false
	}
	s, ok := raw.(string)
	return s, ok
}

func reportPersistSession(runObj *jsonx.Object, task EvalTask, artifactDirectory string) error {
	artifacts := reportGetObject(runObj, "artifacts")
	session, ok := reportGetString(artifacts, PiSessionSnapshotArtifact)
	if !ok {
		return nil
	}
	identity := jsonx.MustStringify([]any{task.EvalSet, task.CaseId, string(task.Variant), task.Model, task.RunNumber})
	sum := sha256.Sum256([]byte(identity))
	directory := filepath.Join(
		artifactDirectory,
		string(task.Variant),
		"sessions",
		hex.EncodeToString(sum[:]),
	)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(directory, "session.jsonl"), []byte(session), 0o600)
}

// ReadTaskObservation parses the Vitest JSON report for task, persists any session snapshot, and returns the observation.
func ReadTaskObservation(task EvalTask, reportPath string, artifactDirectory string) (EvalObservation, error) {
	identity := reportTaskIdentity(task)
	data, err := os.ReadFile(reportPath)
	if err != nil {
		return reportObservationFromIdentity(identity, EvalOutcomeErrored), nil
	}
	parsed, err := jsonx.Parse(data)
	if err != nil {
		return reportObservationFromIdentity(identity, EvalOutcomeErrored), nil
	}
	rootObj, ok := parsed.(*jsonx.Object)
	if !ok || rootObj == nil {
		return reportObservationFromIdentity(identity, EvalOutcomeErrored), nil
	}
	rawTestResults, hasTestResults := rootObj.Get("testResults")
	testResults, ok := rawTestResults.([]any)
	if !hasTestResults || !ok {
		return reportObservationFromIdentity(identity, EvalOutcomeErrored), nil
	}

	reportedFullName := task.EvalSet + " " + task.CaseId
	var assertions []*jsonx.Object
	for _, rawSuite := range testResults {
		suiteObj, ok := rawSuite.(*jsonx.Object)
		if !ok || suiteObj == nil {
			return reportObservationFromIdentity(identity, EvalOutcomeErrored), nil
		}
		rawAssertions, hasAssertions := suiteObj.Get("assertionResults")
		suiteAssertions, ok := rawAssertions.([]any)
		if !hasAssertions || !ok {
			return reportObservationFromIdentity(identity, EvalOutcomeErrored), nil
		}
		for _, rawAssertion := range suiteAssertions {
			assertionObj, ok := rawAssertion.(*jsonx.Object)
			if !ok || assertionObj == nil {
				continue
			}
			fullName, ok := reportGetString(assertionObj, "fullName")
			if ok && fullName == reportedFullName {
				assertions = append(assertions, assertionObj)
			}
		}
	}
	if len(assertions) != 1 {
		return reportObservationFromIdentity(identity, EvalOutcomeErrored), nil
	}

	assertion := assertions[0]
	status, ok := reportGetString(assertion, "status")
	if !ok {
		return reportObservationFromIdentity(identity, EvalOutcomeErrored), nil
	}
	statusOutcome := ClassifyCaseStatus(status)
	if statusOutcome == EvalOutcomeSkipped || statusOutcome == EvalOutcomePending {
		return reportObservationFromIdentity(identity, statusOutcome), nil
	}

	meta := reportGetObject(assertion, "meta")
	harnessObj := reportGetObject(meta, "harness")
	runObj := reportGetObject(harnessObj, "run")
	if runObj == nil {
		return reportObservationFromIdentity(identity, EvalOutcomeErrored), nil
	}

	if err := reportPersistSession(runObj, task, artifactDirectory); err != nil {
		return reportObservationFromIdentity(identity, EvalOutcomeErrored), err
	}

	usageObj := reportGetObject(runObj, "usage")
	if usageObj == nil {
		return reportObservationFromIdentity(identity, EvalOutcomeErrored), nil
	}
	provider, hasProvider := reportGetString(usageObj, "provider")
	model, hasModel := reportGetString(usageObj, "model")
	actualModel := ""
	if hasProvider && hasModel && provider != "" && model != "" {
		actualModel = provider + "/" + model
	}
	if actualModel != task.Model {
		return reportObservationFromIdentity(identity, EvalOutcomeErrored), nil
	}

	usageMeta := reportGetObject(usageObj, "metadata")
	timingsObj := reportGetObject(runObj, "timings")
	inputTokens, err := reportOptionalMetric(usageObj, "inputTokens")
	if err != nil {
		return reportObservationFromIdentity(identity, EvalOutcomeErrored), nil
	}
	outputTokens, err := reportOptionalMetric(usageObj, "outputTokens")
	if err != nil {
		return reportObservationFromIdentity(identity, EvalOutcomeErrored), nil
	}
	cacheReadTokens, err := reportOptionalMetric(usageMeta, "cacheReadTokens")
	if err != nil {
		return reportObservationFromIdentity(identity, EvalOutcomeErrored), nil
	}
	cacheWriteTokens, err := reportOptionalMetric(usageMeta, "cacheWriteTokens")
	if err != nil {
		return reportObservationFromIdentity(identity, EvalOutcomeErrored), nil
	}
	totalTokens, err := reportOptionalMetric(usageObj, "totalTokens")
	if err != nil {
		return reportObservationFromIdentity(identity, EvalOutcomeErrored), nil
	}
	toolCalls, err := reportOptionalMetric(usageObj, "toolCalls")
	if err != nil {
		return reportObservationFromIdentity(identity, EvalOutcomeErrored), nil
	}
	totalMs, err := reportOptionalMetric(timingsObj, "totalMs")
	if err != nil {
		return reportObservationFromIdentity(identity, EvalOutcomeErrored), nil
	}
	estimatedCostUsd, err := reportOptionalMetric(usageMeta, "estimatedCostUsd")
	if err != nil {
		return reportObservationFromIdentity(identity, EvalOutcomeErrored), nil
	}

	obs := EvalObservation{
		EvalSet:          identity.EvalSet,
		CaseId:           identity.CaseId,
		Variant:          identity.Variant,
		Model:            identity.Model,
		RunNumber:        identity.RunNumber,
		InputTokens:      inputTokens,
		OutputTokens:     outputTokens,
		CacheReadTokens:  cacheReadTokens,
		CacheWriteTokens: cacheWriteTokens,
		TotalTokens:      totalTokens,
		ToolCalls:        toolCalls,
		TotalMs:          totalMs,
		EstimatedCostUsd: estimatedCostUsd,
	}

	rawErrors, hasErrors := runObj.Get("errors")
	if !hasErrors {
		obs.Outcome = EvalOutcomeErrored
		return obs, nil
	}
	errList, ok := rawErrors.([]any)
	if !ok || statusOutcome == EvalOutcomeErrored || len(errList) > 0 {
		obs.Outcome = EvalOutcomeErrored
		return obs, nil
	}

	evalObj := reportGetObject(meta, "eval")
	score, err := reportValidateScore(evalObj)
	if err != nil {
		obs.Outcome = EvalOutcomeErrored
		return obs, nil
	}
	if score == nil {
		obs.Outcome = EvalOutcomeUnscored
		return obs, nil
	}
	obs.Outcome = EvalOutcomeScored
	obs.Score = score
	return obs, nil
}

const (
	reportControl   = DocumentationVariantWithoutDocs
	reportTreatment = DocumentationVariantWithDocs
)

type reportPair struct {
	control   EvalObservation
	treatment EvalObservation
}

type reportPairGroup struct {
	evalSet      string
	caseId       string
	model        string
	runNumber    int
	expected     map[DocumentationVariant]int
	observations map[DocumentationVariant][]EvalObservation
}

func reportMean(values []float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	sum := 0.0
	for _, value := range values {
		sum += value
	}
	m := sum / float64(len(values))
	return &m
}

func reportDifference(treatment float64, control float64) float64 {
	diff := treatment - control
	formatted := strconv.FormatFloat(diff, 'g', 15, 64)
	rounded, err := strconv.ParseFloat(formatted, 64)
	if err != nil {
		return diff
	}
	if rounded == 0 {
		return 0
	}
	return rounded
}

func reportPairKey(evalSet string, caseId string, model string, runNumber int) string {
	return jsonx.MustStringify([]any{evalSet, caseId, model, runNumber})
}

func reportGroupPairs(expectedRuns []ExpectedEvalRun, observations []EvalObservation) []*reportPairGroup {
	groups := make(map[string]*reportPairGroup)
	getGroup := func(evalSet, caseId, model string, runNumber int) *reportPairGroup {
		key := reportPairKey(evalSet, caseId, model, runNumber)
		if existing, ok := groups[key]; ok {
			return existing
		}
		group := &reportPairGroup{
			evalSet:      evalSet,
			caseId:       caseId,
			model:        model,
			runNumber:    runNumber,
			expected:     make(map[DocumentationVariant]int),
			observations: make(map[DocumentationVariant][]EvalObservation),
		}
		groups[key] = group
		return group
	}
	for _, expected := range expectedRuns {
		group := getGroup(expected.EvalSet, expected.CaseId, expected.Model, expected.RunNumber)
		group.expected[expected.Variant]++
	}
	for _, observation := range observations {
		group := getGroup(observation.EvalSet, observation.CaseId, observation.Model, observation.RunNumber)
		group.observations[observation.Variant] = append(group.observations[observation.Variant], observation)
	}
	sorted := make([]*reportPairGroup, 0, len(groups))
	for _, group := range groups {
		sorted = append(sorted, group)
	}
	slices.SortFunc(sorted, func(left, right *reportPairGroup) int {
		return cmp.Or(
			cmp.Compare(left.evalSet, right.evalSet),
			cmp.Compare(left.caseId, right.caseId),
			cmp.Compare(left.model, right.model),
			cmp.Compare(left.runNumber, right.runNumber),
		)
	})
	return sorted
}

func reportResolvePair(group *reportPairGroup) (*reportPair, *BlockedPair) {
	reasons := make([]string, 0)
	for _, variant := range DocumentationVariants {
		expected := group.expected[variant]
		observed := group.observations[variant]
		if expected != 1 {
			reasons = append(reasons, fmt.Sprintf("%s: design expected 1 run, found %d", variant, expected))
		}
		if len(observed) != expected {
			plural := "s"
			if expected == 1 {
				plural = ""
			}
			reasons = append(reasons, fmt.Sprintf("%s: expected %d observation%s, found %d", variant, expected, plural, len(observed)))
		}
		if expected == 1 && len(observed) == 1 && observed[0].Outcome != EvalOutcomeScored {
			reasons = append(reasons, fmt.Sprintf("%s: %s", variant, observed[0].Outcome))
		}
	}
	if len(reasons) > 0 {
		return nil, &BlockedPair{
			EvalSet:   group.evalSet,
			CaseId:    group.caseId,
			Model:     group.model,
			RunNumber: group.runNumber,
			Reasons:   reasons,
		}
	}
	return &reportPair{
		control:   group.observations[reportControl][0],
		treatment: group.observations[reportTreatment][0],
	}, nil
}

func reportSummarizeMetric(pairs []reportPair, selector func(EvalObservation) *float64) PairedMetricSummary {
	var control []float64
	var treatment []float64
	for _, pair := range pairs {
		cv := selector(pair.control)
		tv := selector(pair.treatment)
		if cv == nil || tv == nil {
			continue
		}
		control = append(control, *cv)
		treatment = append(treatment, *tv)
	}
	controlMean := reportMean(control)
	treatmentMean := reportMean(treatment)
	var meanDelta *float64
	if controlMean != nil && treatmentMean != nil {
		d := reportDifference(*treatmentMean, *controlMean)
		meanDelta = &d
	}
	return PairedMetricSummary{
		EligiblePairs: len(control),
		ControlMean:   controlMean,
		TreatmentMean: treatmentMean,
		MeanDelta:     meanDelta,
	}
}

func reportOperationalTotal(runs []EvalObservation, selector func(EvalObservation) *float64) OperationalMetricTotal {
	var values []float64
	for _, run := range runs {
		if v := selector(run); v != nil {
			values = append(values, *v)
		}
	}
	if len(values) == 0 {
		return OperationalMetricTotal{AvailableRuns: 0, Total: nil}
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return OperationalMetricTotal{AvailableRuns: len(values), Total: &sum}
}

func reportVariantTotals(observations []EvalObservation, variant DocumentationVariant) VariantTotals {
	var runs []EvalObservation
	for _, obs := range observations {
		if obs.Variant == variant {
			runs = append(runs, obs)
		}
	}
	return VariantTotals{
		Variant:          variant,
		Runs:             len(runs),
		InputTokens:      reportOperationalTotal(runs, func(o EvalObservation) *float64 { return o.InputTokens }),
		OutputTokens:     reportOperationalTotal(runs, func(o EvalObservation) *float64 { return o.OutputTokens }),
		CacheReadTokens:  reportOperationalTotal(runs, func(o EvalObservation) *float64 { return o.CacheReadTokens }),
		CacheWriteTokens: reportOperationalTotal(runs, func(o EvalObservation) *float64 { return o.CacheWriteTokens }),
		TotalTokens:      reportOperationalTotal(runs, func(o EvalObservation) *float64 { return o.TotalTokens }),
		ToolCalls:        reportOperationalTotal(runs, func(o EvalObservation) *float64 { return o.ToolCalls }),
		TotalMs:          reportOperationalTotal(runs, func(o EvalObservation) *float64 { return o.TotalMs }),
		EstimatedCostUsd: reportOperationalTotal(runs, func(o EvalObservation) *float64 { return o.EstimatedCostUsd }),
	}
}

func reportComparisonFlags(pairs []reportPair, controlPassRate, treatmentPassRate *float64) []string {
	flags := make([]string, 0)
	if controlPassRate != nil && treatmentPassRate != nil {
		if *controlPassRate == *treatmentPassRate {
			flags = append(flags, "no-lift")
		}
		if *treatmentPassRate < *controlPassRate {
			flags = append(flags, "negative-delta")
		}
		if *controlPassRate == 1 {
			flags = append(flags, "control-saturated")
		}
		if *treatmentPassRate == 1 {
			flags = append(flags, "treatment-saturated")
		}
	}
	outcomes := make(map[string]map[bool]struct{})
	addOutcome := func(obs EvalObservation) {
		key := jsonx.MustStringify([]string{obs.CaseId, string(obs.Variant)})
		set, ok := outcomes[key]
		if !ok {
			set = make(map[bool]struct{})
			outcomes[key] = set
		}
		passed := obs.Score != nil && *obs.Score >= 1
		set[passed] = struct{}{}
	}
	for _, pair := range pairs {
		addOutcome(pair.control)
		addOutcome(pair.treatment)
	}
	for _, set := range outcomes {
		if len(set) > 1 {
			flags = append(flags, "flaky")
			break
		}
	}
	return flags
}

// SummarizeEvalObservations computes paired documentation comparisons, blocked pairs, and operational totals.
func SummarizeEvalObservations(
	protocolDigest string,
	expectedRuns []ExpectedEvalRun,
	observations []EvalObservation,
) EvalComparisonReport {
	groups := reportGroupPairs(expectedRuns, observations)
	blockedPairs := make([]BlockedPair, 0)
	pairsByEvalSet := make(map[string][]reportPair)
	totalsByEvalSet := make(map[string]int)
	for _, group := range groups {
		totalsByEvalSet[group.evalSet]++
		pair, blocked := reportResolvePair(group)
		if blocked != nil {
			blockedPairs = append(blockedPairs, *blocked)
		}
		if pair != nil {
			pairsByEvalSet[group.evalSet] = append(pairsByEvalSet[group.evalSet], *pair)
		}
	}

	evalSets := make([]string, 0, len(totalsByEvalSet))
	for evalSet := range totalsByEvalSet {
		evalSets = append(evalSets, evalSet)
	}
	slices.Sort(evalSets)

	comparisons := make([]EvalSetComparison, 0, len(evalSets))
	for _, evalSet := range evalSets {
		totalPairs := totalsByEvalSet[evalSet]
		pairs := pairsByEvalSet[evalSet]
		blockedCount := totalPairs - len(pairs)
		var controlPassRate, treatmentPassRate, lift *float64
		if blockedCount == 0 && len(pairs) > 0 {
			controlPass := 0
			treatmentPass := 0
			for _, p := range pairs {
				if p.control.Score != nil && *p.control.Score >= 1 {
					controlPass++
				}
				if p.treatment.Score != nil && *p.treatment.Score >= 1 {
					treatmentPass++
				}
			}
			c := float64(controlPass) / float64(len(pairs))
			t := float64(treatmentPass) / float64(len(pairs))
			l := reportDifference(t, c)
			controlPassRate = &c
			treatmentPassRate = &t
			lift = &l
		}
		comparisons = append(comparisons, EvalSetComparison{
			EvalSet:           evalSet,
			TotalPairs:        totalPairs,
			EligiblePairs:     len(pairs),
			BlockedPairs:      blockedCount,
			ControlPassRate:   controlPassRate,
			TreatmentPassRate: treatmentPassRate,
			Lift:              lift,
			Flags:             reportComparisonFlags(pairs, controlPassRate, treatmentPassRate),
			TotalTokens:       reportSummarizeMetric(pairs, func(o EvalObservation) *float64 { return o.TotalTokens }),
			ToolCalls:         reportSummarizeMetric(pairs, func(o EvalObservation) *float64 { return o.ToolCalls }),
			TotalMs:           reportSummarizeMetric(pairs, func(o EvalObservation) *float64 { return o.TotalMs }),
			EstimatedCostUsd:  reportSummarizeMetric(pairs, func(o EvalObservation) *float64 { return o.EstimatedCostUsd }),
		})
	}

	return EvalComparisonReport{
		SchemaVersion:  3,
		ProtocolDigest: protocolDigest,
		Control:        reportControl,
		Treatment:      reportTreatment,
		Comparisons:    comparisons,
		BlockedPairs:   blockedPairs,
		OperationalTotals: []VariantTotals{
			reportVariantTotals(observations, reportControl),
			reportVariantTotals(observations, reportTreatment),
		},
	}
}

func reportPercentage(value *float64) string {
	if value == nil {
		return "unavailable"
	}
	return fmt.Sprintf("%.1f%%", *value*100)
}

func reportSigned(value float64, digits int) string {
	sign := ""
	if value >= 0 {
		sign = "+"
	}
	return fmt.Sprintf("%s%.*f", sign, digits, value)
}

func reportPairedMetric(label string, metric PairedMetricSummary, unit string) string {
	if metric.MeanDelta == nil || metric.ControlMean == nil || metric.TreatmentMean == nil {
		return fmt.Sprintf("    %10s  unavailable", label)
	}
	return fmt.Sprintf(
		"    %10s  %s%s (with %.1f%s, without %.1f%s, %d pairs)",
		label,
		reportSigned(*metric.MeanDelta, 1),
		unit,
		*metric.TreatmentMean,
		unit,
		*metric.ControlMean,
		unit,
		metric.EligiblePairs,
	)
}

func reportOperationalMetric(metric OperationalMetricTotal, runs int, format func(float64) string) string {
	if metric.Total == nil {
		return fmt.Sprintf("unavailable (0/%d measured)", runs)
	}
	coverage := ""
	if metric.AvailableRuns != runs {
		coverage = fmt.Sprintf(" (%d/%d measured)", metric.AvailableRuns, runs)
	}
	return format(*metric.Total) + coverage
}

// FormatEvalComparisonReport formats an EvalComparisonReport into a human-readable summary string.
func FormatEvalComparisonReport(report EvalComparisonReport) string {
	if len(report.Comparisons) == 0 {
		return ""
	}
	lines := []string{ansi.Bold("Documentation Eval Comparisons")}
	for _, comparison := range report.Comparisons {
		lines = append(lines, "  "+comparison.EvalSet)
		lines = append(lines, fmt.Sprintf("         Pairs  %d/%d eligible", comparison.EligiblePairs, comparison.TotalPairs))
		if comparison.Lift == nil {
			if comparison.BlockedPairs > 0 {
				lines = append(lines, "     Pass rate  withheld because pairs are blocked")
			} else {
				lines = append(lines, "     Pass rate  unavailable")
			}
		} else {
			lines = append(lines, fmt.Sprintf(
				"     Pass rate  %s pp (with %s, without %s)",
				reportSigned(*comparison.Lift*100, 1),
				reportPercentage(comparison.TreatmentPassRate),
				reportPercentage(comparison.ControlPassRate),
			))
		}
		if len(comparison.Flags) > 0 {
			lines = append(lines, "         Flags  "+strings.Join(comparison.Flags, ", "))
		}
		lines = append(lines, reportPairedMetric("Tokens", comparison.TotalTokens, ""))
		lines = append(lines, reportPairedMetric("Tools", comparison.ToolCalls, ""))
		lines = append(lines, reportPairedMetric("Latency", comparison.TotalMs, "ms"))
		cost := comparison.EstimatedCostUsd
		if cost.MeanDelta == nil || cost.ControlMean == nil || cost.TreatmentMean == nil {
			lines = append(lines, "     Est. cost  unavailable")
		} else {
			sign := "+"
			if *cost.MeanDelta < 0 {
				sign = "-"
			}
			lines = append(lines, fmt.Sprintf(
				"     Est. cost  %s$%.4f (with $%.4f, without $%.4f, %d pairs)",
				sign,
				math.Abs(*cost.MeanDelta),
				*cost.TreatmentMean,
				*cost.ControlMean,
				cost.EligiblePairs,
			))
		}
	}
	lines = append(lines, "  Operational totals")
	for _, totals := range report.OperationalTotals {
		tokens := reportOperationalMetric(totals.TotalTokens, totals.Runs, func(total float64) string {
			return jsonx.MustStringify(total) + " tokens"
		})
		tools := reportOperationalMetric(totals.ToolCalls, totals.Runs, func(total float64) string {
			return jsonx.MustStringify(total) + " tools"
		})
		latency := reportOperationalMetric(totals.TotalMs, totals.Runs, func(total float64) string {
			return fmt.Sprintf("%.2fs", total/1000)
		})
		cost := reportOperationalMetric(totals.EstimatedCostUsd, totals.Runs, func(total float64) string {
			return fmt.Sprintf("$%.4f cost", total)
		})
		lines = append(lines, fmt.Sprintf("    %s: %d runs, %s, %s, %s, %s", totals.Variant, totals.Runs, tokens, tools, latency, cost))
	}
	if len(report.BlockedPairs) > 0 {
		lines = append(lines, "  Blocked pairs")
		for _, blocked := range report.BlockedPairs {
			lines = append(lines, fmt.Sprintf(
				"    %s/%s/%s/run-%d: %s",
				blocked.EvalSet,
				blocked.CaseId,
				blocked.Model,
				blocked.RunNumber,
				strings.Join(blocked.Reasons, "; "),
			))
		}
	}
	return strings.Join(lines, "\n")
}
