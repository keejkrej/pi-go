// Ported from packages/evals/evals/models.docs.eval.ts (pi v1.0.0).

package suites

import (
	"fmt"

	"github.com/keejkrej/pi-go/evals"
)

const (
	mdocsProviderId = "openai"
	mdocsModelId    = "fixture-chat"
	mdocsModelName  = "Fixture Chat"
)

// ModelsDocsEvalSuite returns the models documentation-lift eval suite ("Add model to existing provider").
func ModelsDocsEvalSuite() *evals.EvalSuite {
	preserved := true
	expected := AddedModelOutput{
		Result: AddedModelResult{
			Model: &ModelFields{
				Id:        mdocsModelId,
				Name:      mdocsModelName,
				Provider:  mdocsProviderId,
				Reasoning: true,
				Input:     []string{"text"},
				Cost: ModelFieldsCost{
					Input:      0,
					Output:     0,
					CacheRead:  0,
					CacheWrite: 0,
				},
				ContextWindow: 32768,
				MaxTokens:     4096,
			},
			ExistingModelsPreserved: &preserved,
		},
	}

	judge := evals.StructuredOutputJudge[evals.PiCodingAgentInput, AddedModelOutput, AddedModelOutput](
		evals.StructuredOutputJudgeOptions[AddedModelOutput]{
			Expected:    expected,
			Match:       "strict",
			AllowExtras: false,
		},
	)

	return evals.DescribeEval(
		"Add model to existing provider",
		evals.DescribeEvalOptions[evals.PiCodingAgentInput, AddedModelOutput]{
			File:    "evals/suites/models_docs_eval.go",
			Project: "docs",
			HarnessFactory: func(runOpts evals.SuiteRunOptions) (evals.Harness[evals.PiCodingAgentInput, AddedModelOutput], error) {
				opts := evals.PiCodingAgentHarnessOptions[AddedModelOutput]{
					Model:             runOpts.Model,
					SetupModelRuntime: runOpts.SetupModelRuntime,
					Output: func(args evals.PiOutputArgs) (AddedModelOutput, error) {
						return InspectAddedModel(args.Ctx, args.Session.ModelRuntime(), mdocsProviderId, mdocsModelId), nil
					},
				}
				if runOpts.Variant != "" {
					return evals.CreatePiDocumentationEvalHarnessForTest(runOpts.Variant, opts)
				}
				return evals.CreatePiDocumentationEvalHarness(opts)
			},
			Judges:         []evals.Judge[evals.PiCodingAgentInput, AddedModelOutput]{judge},
			JudgeThreshold: nil,
		},
		func(it *evals.EvalCaseBuilder[evals.PiCodingAgentInput, AddedModelOutput]) {
			it.It("adds the model without replacing existing models", func(rc evals.EvalRunContext[evals.PiCodingAgentInput, AddedModelOutput]) error {
				_, err := rc.Run(evals.StepsInput(
					evals.PiInputStep{
						Type: evals.PiInputStepPrompt,
						Content: fmt.Sprintf(
							"Configure this running Pi installation with a new `%s/%s` model. Do not create project-local configuration. Show it as “%s”. It accepts text, supports reasoning, has a 32,768-token context window and a 4,096-token maximum output, and has no usage cost.",
							mdocsProviderId,
							mdocsModelId,
							mdocsModelName,
						),
					},
					evals.PiInputStep{Type: evals.PiInputStepReload},
				))
				return err
			})
		},
	)
}
