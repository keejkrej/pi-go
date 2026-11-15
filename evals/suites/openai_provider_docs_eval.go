// Ported from packages/evals/evals/openai-provider.docs.eval.ts (pi v1.0.0).

package suites

import (
	"context"
	"fmt"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/evals"
)

// OpenaiProviderDocsEvalSuite returns the OpenAI-compatible provider documentation-lift eval suite.
func OpenaiProviderDocsEvalSuite() *evals.EvalSuite {
	server := CreateAcmeServer(AcmeServerModeOpenai)

	validReq := true
	expected := ProviderRuntimeOutput{
		Result: ProviderRuntimeResult{
			ValidRequestReceived: &validReq,
			Model: &ModelFields{
				Id:        OpenaiModelId,
				Name:      "Acme Chat",
				Provider:  OpenaiProviderId,
				Reasoning: false,
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
			Response: &ProviderRuntimeResponse{
				Text:         OpenaiProbeResponse,
				StopReason:   "stop",
				InputTokens:  3,
				OutputTokens: 2,
			},
		},
	}

	judge := evals.StructuredOutputJudge[evals.PiCodingAgentInput, ProviderRuntimeOutput, ProviderRuntimeOutput](
		evals.StructuredOutputJudgeOptions[ProviderRuntimeOutput]{
			Expected:    expected,
			Match:       "strict",
			AllowExtras: false,
		},
	)

	return evals.DescribeEval(
		"Add OpenAI-compatible provider",
		evals.DescribeEvalOptions[evals.PiCodingAgentInput, ProviderRuntimeOutput]{
			File:    "evals/suites/openai_provider_docs_eval.go",
			Project: "docs",
			HarnessFactory: func(runOpts evals.SuiteRunOptions) (evals.Harness[evals.PiCodingAgentInput, ProviderRuntimeOutput], error) {
				maxTokens := 32
				opts := evals.PiCodingAgentHarnessOptions[ProviderRuntimeOutput]{
					Model:             runOpts.Model,
					SetupModelRuntime: runOpts.SetupModelRuntime,
					Output: func(args evals.PiOutputArgs) (ProviderRuntimeOutput, error) {
						return InspectProvider(args.Ctx, args.Session.ModelRuntime(), ProviderScenario{
							ProviderId: OpenaiProviderId,
							ModelId:    OpenaiModelId,
							CreateContext: func() *ai.Context {
								prompt := OpenaiProbePrompt
								return &ai.Context{
									Messages: ai.MessageList{
										&ai.UserMessage{
											Role:      "user",
											Content:   ai.UserMessageContent{Text: &prompt},
											Timestamp: 0,
										},
									},
								}
							},
							Options: &ai.ModelsSimpleStreamOptions{
								SimpleStreamOptions: ai.SimpleStreamOptions{
									StreamOptions: ai.StreamOptions{
										ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
											Env: ai.ProviderEnv{"ACME_API_KEY": "resolved-acme-key"},
										},
										MaxTokens: &maxTokens,
									},
								},
							},
							ValidRequestReceived: server.ValidRequestReceived,
						}), nil
					},
				}
				if runOpts.Variant != "" {
					return evals.CreatePiDocumentationEvalHarnessForTest(runOpts.Variant, opts)
				}
				return evals.CreatePiDocumentationEvalHarness(opts)
			},
			Judges:         []evals.Judge[evals.PiCodingAgentInput, ProviderRuntimeOutput]{judge},
			JudgeThreshold: nil,
		},
		func(it *evals.EvalCaseBuilder[evals.PiCodingAgentInput, ProviderRuntimeOutput]) {
			it.BeforeAll(func(_ context.Context) error {
				return server.Start()
			})
			it.BeforeEach(func(_ context.Context) error {
				server.Reset()
				return nil
			})
			it.AfterAll(func(_ context.Context) error {
				return server.Stop()
			})
			it.It("configures a provider that works through Pi", func(rc evals.EvalRunContext[evals.PiCodingAgentInput, ProviderRuntimeOutput]) error {
				_, err := rc.Run(evals.StepsInput(
					evals.PiInputStep{
						Type: evals.PiInputStepPrompt,
						Content: fmt.Sprintf(
							"Configure this running Pi installation with Acme as a provider. Do not create project-local configuration. Its provider ID is %s, its API is at %s, and it uses OpenAI Chat Completions. Read its API key from the ACME_API_KEY environment variable.\n\nThe provider offers one model, %s, shown as “Acme Chat”. It accepts text, does not support reasoning, has a 32,768-token context window and a 4,096-token maximum output, and has no usage cost.",
							OpenaiProviderId,
							server.BaseUrl(),
							OpenaiModelId,
						),
					},
					evals.PiInputStep{Type: evals.PiInputStepReload},
				))
				return err
			})
		},
	)
}
