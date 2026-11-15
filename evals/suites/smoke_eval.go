// Ported from packages/evals/evals/smoke.eval.ts (pi v1.0.0).

package suites

import (
	"fmt"
	"os"
	"strings"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/evals"
)

// SmokeEvalSuite returns the host smoke eval suite ("Answer a basic prompt").
func SmokeEvalSuite() *evals.EvalSuite {
	return evals.DescribeEval(
		"Answer a basic prompt",
		evals.DescribeEvalOptions[evals.PiCodingAgentInput, string]{
			File:    "evals/suites/smoke_eval.go",
			Project: "host",
			HarnessFactory: func(runOpts evals.SuiteRunOptions) (evals.Harness[evals.PiCodingAgentInput, string], error) {
				opts := evals.PiCodingAgentHarnessOptions[string]{
					Model:             runOpts.Model,
					NoTools:           core.NoToolsAll,
					SetupModelRuntime: runOpts.SetupModelRuntime,
				}
				return evals.CreatePiCodingAgentHarness(opts), nil
			},
		},
		func(it *evals.EvalCaseBuilder[evals.PiCodingAgentInput, string]) {
			it.It("returns the expected answer", func(rc evals.EvalRunContext[evals.PiCodingAgentInput, string]) error {
				result, err := rc.Run(evals.PromptInput("What's the capital of France? Respond with only the city name."))
				if err != nil {
					return err
				}
				if got := strings.TrimSpace(result.Output); got != "Paris" {
					return fmt.Errorf("output = %q, want %q", got, "Paris")
				}
				if len(result.Errors) != 0 {
					return fmt.Errorf("expected no errors, got %v", result.Errors)
				}
				if result.Usage == nil {
					return fmt.Errorf("expected usage summary, got nil")
				}
				expectedProvider := strings.TrimSpace(os.Getenv("PI_PROVIDER"))
				expectedModel := strings.TrimSpace(os.Getenv("PI_MODEL"))
				if expectedProvider != "" && result.Usage.Provider != expectedProvider {
					return fmt.Errorf("usage.provider = %q, want %q", result.Usage.Provider, expectedProvider)
				}
				if expectedModel != "" && result.Usage.Model != expectedModel {
					return fmt.Errorf("usage.model = %q, want %q", result.Usage.Model, expectedModel)
				}
				if result.Usage.TotalTokens <= 0 {
					return fmt.Errorf("usage.totalTokens = %g, want > 0", result.Usage.TotalTokens)
				}
				return nil
			})
		},
	)
}

// AllSuites returns every eval suite registered in evals/suites across all projects ("host", "docs", "audit").
func AllSuites() []*evals.EvalSuite {
	return []*evals.EvalSuite{
		SmokeEvalSuite(),
		ModelsDocsEvalSuite(),
		CustomProviderDocsEvalSuite(),
		OpenaiProviderDocsEvalSuite(),
		ExtensionsDocsEvalSuite(),
		TuiDocsEvalSuite(),
		DocumentationAuditEvalSuite(),
	}
}
