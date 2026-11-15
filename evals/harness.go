// Ported from packages/evals/src/harness.ts (pi v1.0.0).

package evals

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// DocumentationEvalTools lists the built-in tools exposed during documentation evals,
// intentionally excluding shell execution and unrestricted network tools.
var DocumentationEvalTools = []string{"read", "write", "edit", "grep", "find", "ls"}

// PiInputStepKind identifies one step in a multi-turn Pi coding agent eval input.
type PiInputStepKind string

const (
	PiInputStepPrompt PiInputStepKind = "prompt"
	PiInputStepReload PiInputStepKind = "reload"
)

// PiInputStep represents a single prompt or session reload action.
type PiInputStep struct {
	Type    PiInputStepKind `json:"type"`
	Content string          `json:"content,omitzero"`
}

// PiCodingAgentInput is the normalized sequence of prompt/reload steps executed by a Pi harness.
type PiCodingAgentInput struct {
	Steps []PiInputStep `json:"steps"`
}

// PromptInput builds a single-prompt PiCodingAgentInput.
func PromptInput(prompt string) PiCodingAgentInput {
	return PiCodingAgentInput{
		Steps: []PiInputStep{{Type: PiInputStepPrompt, Content: prompt}},
	}
}

// StepsInput builds a multi-step PiCodingAgentInput.
func StepsInput(steps ...PiInputStep) PiCodingAgentInput {
	return PiCodingAgentInput{Steps: slices.Clone(steps)}
}

// PiCodingAgentModelSelection identifies a provider and model id.
type PiCodingAgentModelSelection struct {
	Provider string `json:"provider"`
	Id       string `json:"id"`
}

// PiOutputArgs is passed to a custom harness output extractor after agent execution finishes.
type PiOutputArgs struct {
	Ctx          context.Context
	Response     string
	Session      *core.AgentSession
	SystemPrompt string
	AgentDir     string
}

// PiCodingAgentHarnessOptions configures CreatePiCodingAgentHarness and CreatePiDocumentationEvalHarness.
type PiCodingAgentHarnessOptions[TOutput any] struct {
	Name                    string
	Model                   *PiCodingAgentModelSelection
	NoTools                 core.NoToolsOption
	Tools                   []*agent.AgentTool
	ToolNames               []string
	CustomTools             []core.ToolDefinition
	WorkspaceFiles          map[string]string
	TransformSystemPrompt   func(defaultPrompt string) string
	ExpectedPiDocumentation *bool
	Output                  func(args PiOutputArgs) (TOutput, error)
	SetupModelRuntime       func(ctx context.Context, runtime *core.ModelRuntime) error
}

// SimpleHarnessResult captures the output, transcript events, usage, timings, and errors from one harness run.
type SimpleHarnessResult[TOutput any] struct {
	Output   TOutput           `json:"output"`
	Events   []TranscriptEvent `json:"events"`
	Usage    *UsageSummary     `json:"usage,omitzero"`
	Metadata *jsonx.Object     `json:"metadata,omitzero"`
	Errors   []HarnessError    `json:"errors"`
	Timings  *TimingsSummary   `json:"timings,omitzero"`
}

// PartialHarnessError attaches captured diagnostics from a failed harness run after transcript/usage capture.
type PartialHarnessError struct {
	Err error
	Run *HarnessRun
}

func (e *PartialHarnessError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e *PartialHarnessError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Harness executes an input under an isolated environment and produces a typed SimpleHarnessResult.
type Harness[TInput any, TOutput any] struct {
	Name string
	Run  func(ctx context.Context, input TInput, setArtifact func(name, value string)) (SimpleHarnessResult[TOutput], error)
}

// JudgeInput carries the input, typed output, and full harness result for scoring.
type JudgeInput[TInput any, TOutput any] struct {
	Input  TInput
	Output TOutput
	Result SimpleHarnessResult[TOutput]
}

// JudgeOutcome holds a normalized [0,1] score (nil if unscored) and optional judge metadata.
type JudgeOutcome struct {
	Score    *float64      `json:"score"`
	Metadata *jsonx.Object `json:"metadata,omitzero"`
}

// Judge evaluates one completed harness run.
type Judge[TInput any, TOutput any] interface {
	Name() string
	Evaluate(ctx context.Context, args JudgeInput[TInput, TOutput]) (JudgeOutcome, error)
}

type harnFuncJudge[TInput any, TOutput any] struct {
	name string
	fn   func(ctx context.Context, args JudgeInput[TInput, TOutput]) (JudgeOutcome, error)
}

func (j harnFuncJudge[TInput, TOutput]) Name() string { return j.name }
func (j harnFuncJudge[TInput, TOutput]) Evaluate(ctx context.Context, args JudgeInput[TInput, TOutput]) (JudgeOutcome, error) {
	return j.fn(ctx, args)
}

// CreateJudge creates a named Judge from an evaluation function.
func CreateJudge[TInput any, TOutput any](
	name string,
	fn func(ctx context.Context, args JudgeInput[TInput, TOutput]) (JudgeOutcome, error),
) Judge[TInput, TOutput] {
	return harnFuncJudge[TInput, TOutput]{name: name, fn: fn}
}

// StructuredOutputJudgeOptions configures StructuredOutputJudge.
type StructuredOutputJudgeOptions[TExpected any] struct {
	Expected    TExpected
	Match       string // "strict" (default) or "partial"
	AllowExtras bool
}

func harnJsonValueEqual(expected, actual any, strict bool, allowExtras bool) (bool, string) {
	switch ev := expected.(type) {
	case nil:
		if actual != nil {
			return false, fmt.Sprintf("expected null, got %s", jsonx.MustStringify(actual))
		}
		return true, ""
	case bool, float64, string:
		if expected != actual {
			return false, fmt.Sprintf("expected %s, got %s", jsonx.MustStringify(expected), jsonx.MustStringify(actual))
		}
		return true, ""
	case []any:
		av, ok := actual.([]any)
		if !ok {
			return false, fmt.Sprintf("expected array, got %s", jsonx.MustStringify(actual))
		}
		if len(ev) != len(av) {
			return false, fmt.Sprintf("expected array length %d, got %d", len(ev), len(av))
		}
		for i := range ev {
			if ok, reason := harnJsonValueEqual(ev[i], av[i], strict, allowExtras); !ok {
				return false, fmt.Sprintf("[%d]: %s", i, reason)
			}
		}
		return true, ""
	case *jsonx.Object:
		if ev == nil {
			if actual != nil {
				return false, fmt.Sprintf("expected null, got %s", jsonx.MustStringify(actual))
			}
			return true, ""
		}
		av, ok := actual.(*jsonx.Object)
		if !ok || av == nil {
			return false, fmt.Sprintf("expected object, got %s", jsonx.MustStringify(actual))
		}
		for _, key := range ev.Keys() {
			expChild, _ := ev.Get(key)
			actChild, exists := av.Get(key)
			if !exists {
				return false, fmt.Sprintf("missing key %q", key)
			}
			if ok, reason := harnJsonValueEqual(expChild, actChild, strict, allowExtras); !ok {
				return false, fmt.Sprintf("%s: %s", key, reason)
			}
		}
		if strict && !allowExtras {
			for _, key := range av.Keys() {
				if !ev.Has(key) {
					return false, fmt.Sprintf("unexpected extra key %q", key)
				}
			}
		}
		return true, ""
	default:
		return expected == actual, "value mismatch"
	}
}

// StructuredOutputJudge scores 1 when output matches opts.Expected structurally via jsonx encoding, and 0 otherwise.
func StructuredOutputJudge[TInput any, TOutput any, TExpected any](
	opts StructuredOutputJudgeOptions[TExpected],
) Judge[TInput, TOutput] {
	strict := opts.Match != "partial"
	return CreateJudge[TInput, TOutput]("StructuredOutputJudge", func(_ context.Context, args JudgeInput[TInput, TOutput]) (JudgeOutcome, error) {
		expVal, err := jsonx.Encode(opts.Expected)
		if err != nil {
			return JudgeOutcome{}, err
		}
		actVal, err := jsonx.Encode(args.Output)
		if err != nil {
			return JudgeOutcome{}, err
		}
		matched, reason := harnJsonValueEqual(expVal, actVal, strict, opts.AllowExtras)
		score := 0.0
		if matched {
			score = 1.0
		}
		meta := jsonx.NewObject()
		meta.Set("matched", matched)
		if reason != "" {
			meta.Set("reason", reason)
		}
		return JudgeOutcome{Score: &score, Metadata: meta}, nil
	})
}

// ExpectedToolCall describes an expected tool invocation for ToolCallJudge.
type ExpectedToolCall struct {
	Name      string
	Arguments any
}

// ToolCallJudgeOptions configures ToolCallJudge.
type ToolCallJudgeOptions struct {
	ExpectedTools []ExpectedToolCall
}

// TranscriptToolCall represents a correlated tool_call and its matching tool_result in transcript order.
type TranscriptToolCall struct {
	Id        string        `json:"id"`
	Name      string        `json:"name"`
	Arguments *jsonx.Object `json:"arguments,omitzero"`
	Status    string        `json:"status"` // "ok" | "error" | "unfinished"
	Error     string        `json:"error,omitzero"`
}

// ToolCalls extracts paired tool invocations and outcomes from a SimpleHarnessResult.
func ToolCalls[TOutput any](result SimpleHarnessResult[TOutput]) []TranscriptToolCall {
	return ToolCallsFromEvents(result.Events)
}

// ToolCallsFromEvents correlates tool_call and tool_result transcript events in order.
func ToolCallsFromEvents(events []TranscriptEvent) []TranscriptToolCall {
	resultsById := make(map[string]TranscriptEvent)
	for _, ev := range events {
		if ev.Type == "tool_result" && ev.ToolCallId != "" {
			resultsById[ev.ToolCallId] = ev
		}
	}
	var calls []TranscriptToolCall
	for _, ev := range events {
		if ev.Type != "tool_call" {
			continue
		}
		call := TranscriptToolCall{
			Id:        ev.Id,
			Name:      ev.Name,
			Arguments: ev.Arguments,
			Status:    "unfinished",
		}
		if res, ok := resultsById[ev.Id]; ok {
			if res.Error != nil {
				call.Status = "error"
				call.Error = res.Error.Message
			} else {
				call.Status = "ok"
			}
		}
		calls = append(calls, call)
	}
	return calls
}

// ToolCallJudge verifies that ExpectedTools appeared in transcript order with matching arguments.
func ToolCallJudge[TInput any, TOutput any](opts ToolCallJudgeOptions) Judge[TInput, TOutput] {
	return CreateJudge[TInput, TOutput]("ToolCallJudge", func(_ context.Context, args JudgeInput[TInput, TOutput]) (JudgeOutcome, error) {
		actual := ToolCalls(args.Result)
		pos := 0
		matched := true
		for _, expected := range opts.ExpectedTools {
			var expArgs any
			hasArgs := expected.Arguments != nil
			if hasArgs {
				var err error
				expArgs, err = jsonx.Encode(expected.Arguments)
				if err != nil {
					return JudgeOutcome{}, err
				}
			}
			found := false
			for pos < len(actual) {
				cand := actual[pos]
				pos++
				if cand.Name != expected.Name {
					continue
				}
				if hasArgs {
					ok, _ := harnJsonValueEqual(expArgs, cand.Arguments, true, false)
					if !ok {
						continue
					}
				}
				found = true
				break
			}
			if !found {
				matched = false
				break
			}
		}
		score := 0.0
		if matched {
			score = 1.0
		}
		meta := jsonx.NewObject()
		meta.Set("matched", matched)
		return JudgeOutcome{Score: &score, Metadata: meta}, nil
	})
}

// SuiteRunOptions supplies optional per-run overrides when executing a registered EvalSuite in Go tests.
type SuiteRunOptions struct {
	Variant           DocumentationVariant
	Model             *PiCodingAgentModelSelection
	SetupModelRuntime func(ctx context.Context, runtime *core.ModelRuntime) error
}

// EvalCaseSummary describes a registered case inside an EvalSuite.
type EvalCaseSummary struct {
	File     string
	Project  string
	EvalSet  string
	CaseId   string
	FullName string
}

// EvalSuite represents an executable collection of evaluation cases mirroring a vitest-evals describeEval block.
type EvalSuite struct {
	File      string
	Project   string
	EvalSet   string
	BeforeAll func(ctx context.Context) error
	AfterAll  func(ctx context.Context) error
	Cases     []harnCompiledCase
}

type harnCompiledCase struct {
	CaseId   string
	FullName string
	Run      func(ctx context.Context, opts SuiteRunOptions) (VitestAssertionResult, error)
}

// Describe returns all case descriptors contained in s.
func (s *EvalSuite) Describe() []EvalCaseSummary {
	if s == nil {
		return nil
	}
	out := make([]EvalCaseSummary, 0, len(s.Cases))
	for _, c := range s.Cases {
		out = append(out, EvalCaseSummary{
			File:     s.File,
			Project:  s.Project,
			EvalSet:  s.EvalSet,
			CaseId:   c.CaseId,
			FullName: c.FullName,
		})
	}
	return out
}

// RunCase executes a single case by CaseId (invoking BeforeAll/AfterAll around the single-case run).
func (s *EvalSuite) RunCase(ctx context.Context, caseId string, opts SuiteRunOptions) (res VitestAssertionResult, err error) {
	if s == nil {
		return VitestAssertionResult{}, fmt.Errorf("EvalSuite is nil")
	}
	if s.BeforeAll != nil {
		if err := s.BeforeAll(ctx); err != nil {
			return VitestAssertionResult{}, err
		}
	}
	defer func() {
		if s.AfterAll != nil {
			afterErr := s.AfterAll(context.Background())
			if err == nil && afterErr != nil {
				err = afterErr
			}
		}
	}()
	for _, c := range s.Cases {
		if c.CaseId == caseId {
			return c.Run(ctx, opts)
		}
	}
	return VitestAssertionResult{}, fmt.Errorf("Eval case %q not found in suite %q", caseId, s.EvalSet)
}

// EvalRunContext provides execution hooks to a suite test body inside DescribeEval.
type EvalRunContext[TInput any, TOutput any] struct {
	Ctx context.Context
	Run func(input TInput) (SimpleHarnessResult[TOutput], error)
}

// EvalCaseBuilder collects cases and lifecycle hooks within DescribeEval.
type EvalCaseBuilder[TInput any, TOutput any] struct {
	beforeAll  []func(ctx context.Context) error
	beforeEach []func(ctx context.Context) error
	afterAll   []func(ctx context.Context) error
	cases      []struct {
		caseId string
		fn     func(ctx EvalRunContext[TInput, TOutput]) error
	}
}

// BeforeAll registers a hook executed once before running cases in this suite.
func (b *EvalCaseBuilder[TInput, TOutput]) BeforeAll(fn func(ctx context.Context) error) {
	b.beforeAll = append(b.beforeAll, fn)
}

// BeforeEach registers a hook executed before each case in this suite.
func (b *EvalCaseBuilder[TInput, TOutput]) BeforeEach(fn func(ctx context.Context) error) {
	b.beforeEach = append(b.beforeEach, fn)
}

// AfterAll registers a teardown hook executed after running cases in this suite.
func (b *EvalCaseBuilder[TInput, TOutput]) AfterAll(fn func(ctx context.Context) error) {
	b.afterAll = append(b.afterAll, fn)
}

// It adds a test case to the suite.
func (b *EvalCaseBuilder[TInput, TOutput]) It(caseId string, fn func(ctx EvalRunContext[TInput, TOutput]) error) {
	b.cases = append(b.cases, struct {
		caseId string
		fn     func(ctx EvalRunContext[TInput, TOutput]) error
	}{caseId: caseId, fn: fn})
}

// DescribeEvalOptions configures DescribeEval.
type DescribeEvalOptions[TInput any, TOutput any] struct {
	File           string
	Project        string
	Harness        *Harness[TInput, TOutput]
	HarnessFactory func(opts SuiteRunOptions) (Harness[TInput, TOutput], error)
	Judges         []Judge[TInput, TOutput]
	JudgeThreshold *float64
}

// DescribeEval defines an EvalSuite matching vitest-evals semantics, deferring harness/server setup until execution.
func DescribeEval[TInput any, TOutput any](
	evalSet string,
	opts DescribeEvalOptions[TInput, TOutput],
	build func(b *EvalCaseBuilder[TInput, TOutput]),
) *EvalSuite {
	builder := &EvalCaseBuilder[TInput, TOutput]{}
	build(builder)

	suite := &EvalSuite{
		File:    opts.File,
		Project: opts.Project,
		EvalSet: evalSet,
	}
	if len(builder.beforeAll) > 0 {
		hooks := slices.Clone(builder.beforeAll)
		suite.BeforeAll = func(ctx context.Context) error {
			for _, h := range hooks {
				if err := h(ctx); err != nil {
					return err
				}
			}
			return nil
		}
	}
	if len(builder.afterAll) > 0 {
		hooks := slices.Clone(builder.afterAll)
		suite.AfterAll = func(ctx context.Context) error {
			var firstErr error
			for _, h := range hooks {
				if err := h(ctx); err != nil && firstErr == nil {
					firstErr = err
				}
			}
			return firstErr
		}
	}

	beforeEach := slices.Clone(builder.beforeEach)
	judges := slices.Clone(opts.Judges)
	for _, item := range builder.cases {
		c := item
		fullName := evalSet + " > " + c.caseId
		suite.Cases = append(suite.Cases, harnCompiledCase{
			CaseId:   c.caseId,
			FullName: fullName,
			Run: func(ctx context.Context, runOpts SuiteRunOptions) (VitestAssertionResult, error) {
				assertion := VitestAssertionResult{
					AncestorTitles:  []string{evalSet},
					FullName:        evalSet + " " + c.caseId,
					Status:          "failed",
					Title:           c.caseId,
					FailureMessages: []string{},
				}
				for _, hook := range beforeEach {
					if err := hook(ctx); err != nil {
						assertion.FailureMessages = append(assertion.FailureMessages, err.Error())
						return assertion, err
					}
				}
				var harness Harness[TInput, TOutput]
				var hErr error
				switch {
				case opts.HarnessFactory != nil:
					harness, hErr = opts.HarnessFactory(runOpts)
				case opts.Harness != nil:
					harness = *opts.Harness
				default:
					hErr = fmt.Errorf("EvalSuite %q has no harness configured", evalSet)
				}
				if hErr != nil {
					assertion.FailureMessages = append(assertion.FailureMessages, hErr.Error())
					return assertion, hErr
				}

				artifacts := make(map[string]string)
				setArtifact := func(name, value string) {
					artifacts[name] = value
				}
				var lastInput TInput
				var lastResult *SimpleHarnessResult[TOutput]
				var runErr error

				runFn := func(input TInput) (SimpleHarnessResult[TOutput], error) {
					lastInput = input
					res, err := harness.Run(ctx, input, setArtifact)
					if err != nil {
						runErr = err
						var partial *PartialHarnessError
						if errors.As(err, &partial) && partial.Run != nil {
							partial.Run.Artifacts = maps.Clone(artifacts)
							assertion.Meta = &VitestReportMeta{
								Harness: &ReportCaseHarness{
									Name: harness.Name,
									Run:  partial.Run,
								},
							}
						}
						return SimpleHarnessResult[TOutput]{}, err
					}
					lastResult = &res
					return res, nil
				}

				caseErr := c.fn(EvalRunContext[TInput, TOutput]{Ctx: ctx, Run: runFn})
				if caseErr != nil && runErr == nil {
					runErr = caseErr
				}
				if lastResult != nil {
					runErrors := slices.Clone(lastResult.Errors)
					if runErrors == nil {
						runErrors = []HarnessError{}
					}
					if runErr != nil && len(runErrors) == 0 {
						runErrors = append(runErrors, HarnessError{Message: runErr.Error()})
					}
					harnessRun := &HarnessRun{
						Output:    lastResult.Output,
						Session:   &HarnessSession{Events: lastResult.Events},
						Usage:     lastResult.Usage,
						Timings:   lastResult.Timings,
						Metadata:  lastResult.Metadata,
						Artifacts: maps.Clone(artifacts),
						Errors:    runErrors,
					}
					assertion.Meta = &VitestReportMeta{
						Harness: &ReportCaseHarness{
							Name: harness.Name,
							Run:  harnessRun,
						},
					}
				}

				if runErr != nil {
					assertion.FailureMessages = append(assertion.FailureMessages, runErr.Error())
					return assertion, runErr
				}
				if lastResult == nil {
					err := fmt.Errorf("Eval case %q finished without running harness", c.caseId)
					assertion.FailureMessages = append(assertion.FailureMessages, err.Error())
					return assertion, err
				}

				if len(judges) > 0 {
					scores := make([]ReportCaseScore, 0, len(judges))
					scoreSum := 0.0
					scoreCount := 0
					for _, judge := range judges {
						outcome, jErr := judge.Evaluate(ctx, JudgeInput[TInput, TOutput]{
							Input:  lastInput,
							Output: lastResult.Output,
							Result: *lastResult,
						})
						if jErr != nil {
							assertion.FailureMessages = append(assertion.FailureMessages, jErr.Error())
							return assertion, jErr
						}
						scores = append(scores, ReportCaseScore{
							Name:     judge.Name(),
							Score:    outcome.Score,
							Metadata: outcome.Metadata,
						})
						if outcome.Score != nil {
							scoreSum += *outcome.Score
							scoreCount++
						}
					}
					var avgScore *float64
					if scoreCount > 0 {
						avg := scoreSum / float64(scoreCount)
						avgScore = &avg
					}
					thresholdFailed := false
					if opts.JudgeThreshold != nil && avgScore != nil && *avgScore < *opts.JudgeThreshold {
						thresholdFailed = true
					}
					if assertion.Meta == nil {
						assertion.Meta = &VitestReportMeta{}
					}
					assertion.Meta.Eval = &ReportCaseEval{
						AvgScore:        avgScore,
						Scores:          scores,
						ThresholdFailed: thresholdFailed,
					}
					if thresholdFailed {
						err := fmt.Errorf("Eval score %.3f below threshold %.3f", *avgScore, *opts.JudgeThreshold)
						assertion.FailureMessages = append(assertion.FailureMessages, err.Error())
						return assertion, err
					}
				}

				assertion.Status = "passed"
				return assertion, nil
			},
		})
	}
	return suite
}

// ResolveModelSelection resolves an explicit model selection or reads PI_PROVIDER and PI_MODEL from environment (or os env if nil).
func ResolveModelSelection(explicit *PiCodingAgentModelSelection, environment map[string]string) (PiCodingAgentModelSelection, error) {
	var provider, id string
	if explicit != nil {
		provider = strings.TrimSpace(explicit.Provider)
		id = strings.TrimSpace(explicit.Id)
	} else if environment != nil {
		provider = strings.TrimSpace(environment["PI_PROVIDER"])
		id = strings.TrimSpace(environment["PI_MODEL"])
	} else {
		provider = strings.TrimSpace(os.Getenv("PI_PROVIDER"))
		id = strings.TrimSpace(os.Getenv("PI_MODEL"))
	}
	if provider == "" || id == "" {
		return PiCodingAgentModelSelection{}, fmt.Errorf("Select a harness model explicitly or set both PI_PROVIDER and PI_MODEL as defaults.")
	}
	return PiCodingAgentModelSelection{Provider: provider, Id: id}, nil
}

// ApplyIsolatedEnvironment removes PI_EVAL_* vars and overrides HOME, USERPROFILE, and PI_CODING_AGENT_DIR,
// returning a closure that restores the prior process environment.
func ApplyIsolatedEnvironment(home string, agentDir string) func() {
	overrides := map[string]string{
		"HOME":                home,
		"USERPROFILE":         home,
		"PI_CODING_AGENT_DIR": agentDir,
	}
	type prevEntry struct {
		val     string
		present bool
	}
	previous := make(map[string]prevEntry)
	for _, entry := range os.Environ() {
		name, val, ok := strings.Cut(entry, "=")
		if !ok || !strings.HasPrefix(name, "PI_EVAL_") {
			continue
		}
		previous[name] = prevEntry{val: val, present: true}
		_ = os.Unsetenv(name)
	}
	for name, val := range overrides {
		if _, already := previous[name]; !already {
			oldVal, oldOk := os.LookupEnv(name)
			previous[name] = prevEntry{val: oldVal, present: oldOk}
		}
		_ = os.Setenv(name, val)
	}
	return func() {
		for name, prev := range previous {
			if !prev.present {
				_ = os.Unsetenv(name)
			} else {
				_ = os.Setenv(name, prev.val)
			}
		}
	}
}

type harnSandboxIdentity struct {
	uid int
	gid int
}

var harnEnvVarNameRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

func harnParseSandboxId(name string) (*int, error) {
	raw, ok := os.LookupEnv(name)
	if !ok {
		return nil, nil
	}
	id, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || id < 1 {
		return nil, fmt.Errorf("%s must be a positive integer.", name)
	}
	return &id, nil
}

func harnResolveSandboxIdentity() (*harnSandboxIdentity, error) {
	uid, err := harnParseSandboxId("PI_EVAL_SANDBOX_UID")
	if err != nil {
		return nil, err
	}
	gid, err := harnParseSandboxId("PI_EVAL_SANDBOX_GID")
	if err != nil {
		return nil, err
	}
	if uid == nil && gid == nil {
		return nil, nil
	}
	if uid == nil || gid == nil {
		return nil, fmt.Errorf("Set both PI_EVAL_SANDBOX_UID and PI_EVAL_SANDBOX_GID, or neither.")
	}
	return &harnSandboxIdentity{uid: *uid, gid: *gid}, nil
}

func harnToTranscriptEvents(messages []agent.AgentMessage) []TranscriptEvent {
	events := make([]TranscriptEvent, 0, len(messages))
	for _, message := range messages {
		if message == nil {
			continue
		}
		switch m := message.(type) {
		case *ai.UserMessage:
			if m == nil {
				continue
			}
			events = append(events, TranscriptEvent{
				Type:    "message",
				Role:    "user",
				Content: ai.ContentText(m.Content),
			})
		case *ai.AssistantMessage:
			if m == nil {
				continue
			}
			if text := ai.ContentText(m.Content); text != "" {
				events = append(events, TranscriptEvent{
					Type:    "message",
					Role:    "assistant",
					Content: text,
				})
			}
			for _, part := range m.Content {
				tc, ok := part.(*ai.ToolCall)
				if !ok || tc == nil {
					continue
				}
				events = append(events, TranscriptEvent{
					Type:      "tool_call",
					Id:        tc.Id,
					Name:      tc.Name,
					Arguments: tc.Arguments,
				})
			}
		case *ai.ToolResultMessage:
			if m == nil {
				continue
			}
			text := ai.ContentText(m.Content)
			allText := true
			for _, part := range m.Content {
				if _, ok := part.(*ai.TextContent); !ok {
					allText = false
					break
				}
			}
			var content any = text
			if !allText {
				if encoded, err := jsonx.Encode(m.Content); err == nil {
					content = encoded
				}
			}
			ev := TranscriptEvent{
				Type:       "tool_result",
				ToolCallId: m.ToolCallId,
				Name:       m.ToolName,
				Content:    content,
			}
			if m.IsError {
				msg := text
				if msg == "" {
					msg = "Tool failed"
				}
				ev.Error = &HarnessError{Message: msg}
			}
			events = append(events, ev)
		}
	}
	return events
}

func harnSeedWorkspace(workspace string, files map[string]string) error {
	for name, content := range files {
		if name == "" || filepath.IsAbs(name) {
			return fmt.Errorf("Invalid workspace fixture path: %s", name)
		}
		path := filepath.Join(workspace, name)
		rel, err := filepath.Rel(workspace, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return fmt.Errorf("Workspace fixture escapes the workspace: %s", name)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			return err
		}
	}
	return nil
}

func harnPromptAgent(ctx context.Context, session *core.AgentSession, input string) (string, error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	previousMessageCount := len(session.Messages())
	if err := session.Prompt(ctx, input); err != nil {
		return "", err
	}
	msgs := session.Messages()
	var assistant *ai.AssistantMessage
	for i := len(msgs) - 1; i >= previousMessageCount && i < len(msgs); i-- {
		if am, ok := msgs[i].(*ai.AssistantMessage); ok && am != nil {
			assistant = am
			break
		}
	}
	if assistant == nil {
		return "", fmt.Errorf("Agent run completed without an assistant message.")
	}
	if assistant.StopReason != ai.StopReasonStop && assistant.StopReason != ai.StopReasonToolUse {
		if assistant.ErrorMessage != nil && *assistant.ErrorMessage != "" {
			return "", errors.New(*assistant.ErrorMessage)
		}
		return "", fmt.Errorf("Agent run ended with unexpected stop reason: %s.", assistant.StopReason)
	}
	output := session.GetLastAssistantText()
	outText := ""
	if output != nil {
		outText = *output
	}
	if outText == "" && assistant.StopReason == ai.StopReasonStop {
		return "", fmt.Errorf("Agent run produced no assistant text.")
	}
	return outText, nil
}

// VerifySystemPrompt asserts whether the rendered system prompt contains Pi rules and matches expectedPiDocumentation.
func VerifySystemPrompt(systemPrompt string, name string, expectedPiDocumentation *bool) (string, error) {
	if expectedPiDocumentation == nil {
		return systemPrompt, nil
	}
	if !strings.Contains(systemPrompt, "\n<rules>\n") {
		return "", fmt.Errorf("Pi system prompt lost its rules in the %s eval variant.", name)
	}
	hasDocumentation := strings.Contains(systemPrompt, "\n<docs>\nPi documentation (read only")
	if hasDocumentation != *expectedPiDocumentation {
		return "", fmt.Errorf("Pi system prompt does not match the %s eval variant.", name)
	}
	return systemPrompt, nil
}

func harnHasPricing(cost ai.ModelCost) bool {
	checkRates := func(r ai.ModelCostRates) bool {
		return r.Input > 0 || r.Output > 0 || r.CacheRead > 0 || r.CacheWrite > 0
	}
	if checkRates(cost.ModelCostRates) {
		return true
	}
	for _, tier := range cost.Tiers {
		if checkRates(tier.ModelCostRates) {
			return true
		}
	}
	return false
}

// RunPiCodingAgent executes a Pi agent session in an isolated temporary workspace and environment.
func RunPiCodingAgent[TOutput any](
	ctx context.Context,
	input PiCodingAgentInput,
	setArtifact func(name, value string),
	options PiCodingAgentHarnessOptions[TOutput],
) (SimpleHarnessResult[TOutput], error) {
	startedAt := time.Now()
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return SimpleHarnessResult[TOutput]{}, ctx.Err()
	}
	if setArtifact == nil {
		setArtifact = func(string, string) {}
	}
	selection, err := ResolveModelSelection(options.Model, nil)
	if err != nil {
		return SimpleHarnessResult[TOutput]{}, err
	}
	hostAgentDir := utils.GetAgentDir()
	sandboxIdentity, err := harnResolveSandboxIdentity()
	if err != nil {
		return SimpleHarnessResult[TOutput]{}, err
	}
	root, err := os.MkdirTemp("", "pi-eval-")
	if err != nil {
		return SimpleHarnessResult[TOutput]{}, err
	}
	workspace := filepath.Join(root, "workspace")
	isolatedHome := filepath.Join(root, "home")
	agentDir := filepath.Join(isolatedHome, ".pi", "agent")

	var extensionFactories []core.InlineExtension
	var forcedSystemPrompt *string
	if options.TransformSystemPrompt != nil {
		transform := options.TransformSystemPrompt
		extensionFactories = append(extensionFactories, core.InlineExtension{
			Name:   "<inline:eval-system-prompt-transform>",
			Hidden: true,
			Factory: func(pi core.ExtensionAPI) error {
				pi.OnBeforeAgentStart(func(_ context.Context, event *core.BeforeAgentStartEvent, _ core.ExtensionContext) (*core.BeforeAgentStartEventResult, error) {
					transformed := transform(event.SystemPrompt)
					forcedSystemPrompt = &transformed
					return &core.BeforeAgentStartEventResult{SystemPrompt: &transformed}, nil
				})
				return nil
			},
		})
	}

	var sessionManager *core.SessionManager
	var session *core.AgentSession
	var modelRuntime *core.ModelRuntime
	var result *SimpleHarnessResult[TOutput]
	var runDiagnostics *SimpleHarnessResult[TOutput]
	var runError error
	var cleanupErrors []error
	var hiddenCredentialEnvName, hiddenCredentialEnvVal string
	hasHiddenCredentialEnv := false

	restoreEnvironment := ApplyIsolatedEnvironment(isolatedHome, agentDir)
	func() {
		defer func() {
			if sessionManager != nil {
				sessionPath := sessionManager.GetSessionFile()
				if sessionPath == nil || *sessionPath == "" {
					cleanupErrors = append(cleanupErrors, fmt.Errorf("Pi eval produced no session file."))
				} else if rawSession, readErr := os.ReadFile(*sessionPath); readErr != nil {
					if os.IsNotExist(readErr) {
						cleanupErrors = append(cleanupErrors, fmt.Errorf("Pi eval produced no session file."))
					} else {
						cleanupErrors = append(cleanupErrors, readErr)
					}
				} else {
					setArtifact(PiSessionSnapshotArtifact, string(rawSession))
				}
			}
			if session != nil {
				session.Dispose()
			}
			if modelRuntime != nil {
				modelRuntime.Close()
			}
			if rmErr := os.RemoveAll(root); rmErr != nil {
				cleanupErrors = append(cleanupErrors, rmErr)
			}
			restoreEnvironment()
			if hasHiddenCredentialEnv {
				_ = os.Setenv(hiddenCredentialEnvName, hiddenCredentialEnvVal)
			}
		}()

		authPath := filepath.Join(hostAgentDir, "auth.json")
		credentials := ai.NewInMemoryCredentialStore()
		storedCredential := core.ReadStoredCredential(selection.Provider, authPath)
		if storedCredential != nil {
			if _, modErr := credentials.Modify(ctx, selection.Provider, func(_ ai.Credential) (ai.Credential, error) {
				return storedCredential, nil
			}, nil); modErr != nil {
				runError = modErr
				return
			}
		}
		modelsPath := filepath.Join(agentDir, "models.json")
		modelRuntime, err = core.CreateModelRuntime(ctx, &core.CreateModelRuntimeOptions{
			Credentials: credentials,
			ModelsPath:  &modelsPath,
		})
		if err != nil {
			runError = err
			return
		}
		if options.SetupModelRuntime != nil {
			if setupErr := options.SetupModelRuntime(ctx, modelRuntime); setupErr != nil {
				runError = setupErr
				return
			}
		}
		if err := os.MkdirAll(workspace, 0o700); err != nil {
			runError = err
			return
		}
		if err := os.MkdirAll(agentDir, 0o700); err != nil {
			runError = err
			return
		}
		if err := harnSeedWorkspace(workspace, options.WorkspaceFiles); err != nil {
			runError = err
			return
		}
		model := modelRuntime.GetModel(selection.Provider, selection.Id)
		if model == nil {
			runError = fmt.Errorf("Eval model not found: %s/%s", selection.Provider, selection.Id)
			return
		}
		auth, err := modelRuntime.GetAuthModel(ctx, model, nil)
		if err != nil {
			runError = err
			return
		}
		if auth == nil {
			runError = fmt.Errorf("Eval model has no configured authentication: %s/%s", selection.Provider, selection.Id)
			return
		}
		if storedCredential == nil && auth.Auth.ApiKey != nil && *auth.Auth.ApiKey != "" {
			if err := modelRuntime.SetRuntimeApiKey(ctx, selection.Provider, *auth.Auth.ApiKey); err != nil {
				runError = err
				return
			}
		}
		if sandboxIdentity != nil {
			_ = os.Remove(authPath)
			if auth.Source != nil && harnEnvVarNameRe.MatchString(*auth.Source) {
				if envVal, ok := os.LookupEnv(*auth.Source); ok && envVal != "" {
					hiddenCredentialEnvName = *auth.Source
					hiddenCredentialEnvVal = envVal
					hasHiddenCredentialEnv = true
					_ = os.Unsetenv(*auth.Source)
				}
			}
		}

		expectedNames := make(map[string]struct{}, len(extensionFactories))
		for _, factory := range extensionFactories {
			clean := strings.TrimPrefix(strings.TrimSpace(factory.Name), utils.BuiltinPathPrefix)
			if clean != "" {
				expectedNames[clean] = struct{}{}
			}
		}
		var disabledBuiltins []string
		for _, name := range core.CollectBuiltinExtensionNames(nil) {
			if _, keep := expectedNames[name]; !keep {
				disabledBuiltins = append(disabledBuiltins, name)
			}
		}

		services, err := core.CreateAgentSessionServices(ctx, &core.CreateAgentSessionServicesOptions{
			Cwd:          workspace,
			AgentDir:     agentDir,
			ModelRuntime: modelRuntime,
			ResourceLoaderOptions: &core.DefaultResourceLoaderOptions{
				ExtensionFactories:        extensionFactories,
				DisabledBuiltinExtensions: disabledBuiltins,
			},
		})
		if err != nil {
			runError = err
			return
		}
		if ctx.Err() != nil {
			runError = ctx.Err()
			return
		}
		sessionManager, err = core.SessionManagerCreate(workspace, filepath.Join(root, "sessions"))
		if err != nil {
			runError = err
			return
		}
		setArtifact("runId", sessionManager.GetSessionId())

		created, err := core.CreateAgentSessionFromServices(ctx, core.CreateAgentSessionFromServicesOptions{
			Services:       services,
			SessionManager: sessionManager,
			Model:          model,
			ThinkingLevel:  agent.ThinkingLevelOff,
			Tools:          options.Tools,
			ToolNames:      options.ToolNames,
			NoTools:        options.NoTools,
			CustomTools:    options.CustomTools,
		})
		if err != nil {
			runError = err
			return
		}
		session = created.Session

		expectedInlinePaths := make(map[string]struct{}, len(extensionFactories))
		for _, factory := range extensionFactories {
			expectedInlinePaths[factory.Name] = struct{}{}
		}
		var unexpectedExtensions []string
		for _, path := range session.ExtensionRunner().GetExtensionPaths() {
			if _, ok := expectedInlinePaths[path]; !ok {
				unexpectedExtensions = append(unexpectedExtensions, path)
			}
		}
		if len(unexpectedExtensions) > 0 {
			runError = fmt.Errorf("Isolated eval loaded unexpected extensions: %s", strings.Join(unexpectedExtensions, ", "))
			return
		}

		if err := harnEnterToolSandbox(root, sandboxIdentity); err != nil {
			runError = err
			return
		}

		var response string
		hasResponse := false
		stopAbort := context.AfterFunc(ctx, func() {
			if session != nil {
				_ = session.Abort(context.Background())
			}
		})
		for _, step := range input.Steps {
			if step.Type == PiInputStepReload {
				if err := session.Reload(ctx); err != nil {
					stopAbort()
					runError = err
					return
				}
				continue
			}
			resp, err := harnPromptAgent(ctx, session, step.Content)
			if err != nil {
				stopAbort()
				runError = err
				return
			}
			response = resp
			hasResponse = true
		}
		stopAbort()
		if !hasResponse {
			runError = fmt.Errorf("Pi eval input must include at least one prompt step.")
			return
		}

		systemPrompt := ""
		if forcedSystemPrompt != nil {
			systemPrompt = *forcedSystemPrompt
		} else {
			systemPrompt = ai.GetCurrentSystemPrompt(core.ConvertToLlm(session.Messages()))
			if systemPrompt == "" {
				systemPrompt = session.SystemPrompt()
			}
		}
		stats := session.GetSessionStats()
		sum := sha256.Sum256([]byte(systemPrompt))
		meta := jsonx.NewObject()
		meta.Set("systemPromptSha256", hex.EncodeToString(sum[:]))
		usageMeta := &UsageMetadata{
			CacheReadTokens:  float64(stats.Tokens.CacheRead),
			CacheWriteTokens: float64(stats.Tokens.CacheWrite),
		}
		if harnHasPricing(model.Cost) {
			cost := stats.Cost
			usageMeta.EstimatedCostUsd = &cost
		}
		runDiagnostics = &SimpleHarnessResult[TOutput]{
			Events:   harnToTranscriptEvents(session.Messages()),
			Metadata: meta,
			Usage: &UsageSummary{
				Provider:     string(model.Provider),
				Model:        model.Id,
				InputTokens:  float64(stats.Tokens.Input),
				OutputTokens: float64(stats.Tokens.Output),
				TotalTokens:  float64(stats.Tokens.Total),
				ToolCalls:    float64(stats.ToolCalls),
				Metadata:     usageMeta,
			},
			Errors: []HarnessError{},
		}

		harnessName := options.Name
		if harnessName == "" {
			harnessName = "pi-coding-agent"
		}
		if _, err := VerifySystemPrompt(systemPrompt, harnessName, options.ExpectedPiDocumentation); err != nil {
			runError = err
			return
		}

		var outVal TOutput
		if options.Output != nil {
			extracted, err := options.Output(PiOutputArgs{
				Ctx:          ctx,
				Response:     response,
				Session:      session,
				SystemPrompt: systemPrompt,
				AgentDir:     agentDir,
			})
			if err != nil {
				runError = err
				return
			}
			outVal = extracted
		} else {
			if sVal, ok := any(response).(TOutput); ok {
				outVal = sVal
			} else {
				runError = fmt.Errorf("PiCodingAgentHarness requires Output extractor for non-string TOutput")
				return
			}
		}

		res := *runDiagnostics
		res.Output = outVal
		result = &res
	}()

	totalMs := float64(time.Since(startedAt).Nanoseconds()) / 1e6
	totalMs = math.Round(totalMs*1000) / 1000

	failure := runError
	switch {
	case runError != nil && len(cleanupErrors) > 0:
		failure = fmt.Errorf("Agent run failed and cleanup also failed: %w", errors.Join(append([]error{runError}, cleanupErrors...)...))
	case len(cleanupErrors) == 1:
		failure = cleanupErrors[0]
	case len(cleanupErrors) > 1:
		failure = fmt.Errorf("Agent cleanup failed: %w", errors.Join(cleanupErrors...))
	}
	if failure != nil {
		if runDiagnostics != nil {
			partialRun := &HarnessRun{
				Output:   nil,
				Session:  &HarnessSession{Events: runDiagnostics.Events},
				Usage:    runDiagnostics.Usage,
				Timings:  &TimingsSummary{TotalMs: totalMs},
				Metadata: runDiagnostics.Metadata,
				Errors:   []HarnessError{{Message: failure.Error()}},
			}
			return SimpleHarnessResult[TOutput]{}, &PartialHarnessError{Err: failure, Run: partialRun}
		}
		return SimpleHarnessResult[TOutput]{}, failure
	}
	if result == nil {
		return SimpleHarnessResult[TOutput]{}, fmt.Errorf("Pi eval completed without a result.")
	}
	result.Timings = &TimingsSummary{TotalMs: totalMs}
	return *result, nil
}

// CreatePiCodingAgentHarness creates a typed Harness running an isolated Pi agent session.
func CreatePiCodingAgentHarness[TOutput any](options ...PiCodingAgentHarnessOptions[TOutput]) Harness[PiCodingAgentInput, TOutput] {
	var opts PiCodingAgentHarnessOptions[TOutput]
	if len(options) > 0 {
		opts = options[0]
	}
	name := opts.Name
	if name == "" {
		name = "pi-coding-agent"
	}
	return Harness[PiCodingAgentInput, TOutput]{
		Name: name,
		Run: func(ctx context.Context, input PiCodingAgentInput, setArtifact func(name, value string)) (SimpleHarnessResult[TOutput], error) {
			return RunPiCodingAgent(ctx, input, setArtifact, opts)
		},
	}
}

// ResolveDocumentationVariant validates PI_EVAL_VARIANT (or an explicit value when non-empty, reading env if nil/unset).
func ResolveDocumentationVariant(value ...string) (DocumentationVariant, error) {
	raw := ""
	if len(value) > 0 {
		raw = value[0]
	} else {
		raw = os.Getenv("PI_EVAL_VARIANT")
	}
	if raw == string(DocumentationVariantWithoutDocs) {
		return DocumentationVariantWithoutDocs, nil
	}
	if raw == string(DocumentationVariantWithDocs) {
		return DocumentationVariantWithDocs, nil
	}
	return "", fmt.Errorf(`PI_EVAL_VARIANT must be "without_docs" or "with_docs".`)
}

// ExcludePiDocumentation removes only the <docs>...</docs> section from the default Pi system prompt.
func ExcludePiDocumentation(defaultPrompt string) string {
	const documentationStartMarker = "\n<docs>\n"
	const documentationEndMarker = "\n</docs>"
	documentationStart := strings.Index(defaultPrompt, documentationStartMarker)
	if documentationStart == -1 {
		panic("Default Pi system prompt has no Pi documentation section.")
	}
	documentationEndOffset := strings.Index(defaultPrompt[documentationStart:], documentationEndMarker)
	if documentationEndOffset == -1 {
		panic("Default Pi system prompt has no complete Pi documentation section.")
	}
	documentationEnd := documentationStart + documentationEndOffset
	cwdStart := strings.LastIndex(defaultPrompt, "\n<cwd>\n")
	if cwdStart < documentationEnd {
		panic("Default Pi system prompt has no working-directory section.")
	}
	return defaultPrompt[:documentationStart] + defaultPrompt[documentationEnd+len(documentationEndMarker):]
}

// ExcludePiDocumentationSafe removes only the <docs>...</docs> section, returning an error instead of panicking.
func ExcludePiDocumentationSafe(defaultPrompt string) (string, error) {
	const documentationStartMarker = "\n<docs>\n"
	const documentationEndMarker = "\n</docs>"
	documentationStart := strings.Index(defaultPrompt, documentationStartMarker)
	if documentationStart == -1 {
		return "", fmt.Errorf("Default Pi system prompt has no Pi documentation section.")
	}
	documentationEndOffset := strings.Index(defaultPrompt[documentationStart:], documentationEndMarker)
	if documentationEndOffset == -1 {
		return "", fmt.Errorf("Default Pi system prompt has no complete Pi documentation section.")
	}
	documentationEnd := documentationStart + documentationEndOffset
	cwdStart := strings.LastIndex(defaultPrompt, "\n<cwd>\n")
	if cwdStart < documentationEnd {
		return "", fmt.Errorf("Default Pi system prompt has no working-directory section.")
	}
	return defaultPrompt[:documentationStart] + defaultPrompt[documentationEnd+len(documentationEndMarker):], nil
}

// CreatePiDocumentationEvalHarness creates a documentation eval harness, failing closed outside the container sandbox.
func CreatePiDocumentationEvalHarness[TOutput any](options ...PiCodingAgentHarnessOptions[TOutput]) (Harness[PiCodingAgentInput, TOutput], error) {
	identity, err := harnResolveSandboxIdentity()
	if os.Getenv("PI_EVAL_CONTAINER") != "1" || err != nil || identity == nil {
		return Harness[PiCodingAgentInput, TOutput]{}, fmt.Errorf("Documentation evals must run in the isolated container sandbox.")
	}
	variant, err := ResolveDocumentationVariant()
	if err != nil {
		return Harness[PiCodingAgentInput, TOutput]{}, err
	}
	var opts PiCodingAgentHarnessOptions[TOutput]
	if len(options) > 0 {
		opts = options[0]
	}
	return harnBuildDocumentationHarness(variant, opts), nil
}

// CreatePiDocumentationEvalHarnessForTest builds a documentation eval harness with an explicit variant for offline testing.
func CreatePiDocumentationEvalHarnessForTest[TOutput any](
	variant DocumentationVariant,
	options PiCodingAgentHarnessOptions[TOutput],
) (Harness[PiCodingAgentInput, TOutput], error) {
	if variant != DocumentationVariantWithoutDocs && variant != DocumentationVariantWithDocs {
		return Harness[PiCodingAgentInput, TOutput]{}, fmt.Errorf(`PI_EVAL_VARIANT must be "without_docs" or "with_docs".`)
	}
	return harnBuildDocumentationHarness(variant, options), nil
}

func harnBuildDocumentationHarness[TOutput any](
	variant DocumentationVariant,
	opts PiCodingAgentHarnessOptions[TOutput],
) Harness[PiCodingAgentInput, TOutput] {
	expectedDocs := variant == DocumentationVariantWithDocs
	opts.Name = string(variant)
	opts.ExpectedPiDocumentation = &expectedDocs
	if opts.ToolNames == nil && opts.Tools == nil {
		opts.ToolNames = slices.Clone(DocumentationEvalTools)
	}
	if variant == DocumentationVariantWithoutDocs {
		opts.TransformSystemPrompt = ExcludePiDocumentation
	} else {
		opts.TransformSystemPrompt = nil
	}
	return CreatePiCodingAgentHarness(opts)
}
