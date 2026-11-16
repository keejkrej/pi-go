// Ported from packages/evals/evals/custom-provider.docs.eval.ts (pi v1.0.0).

package suites

import (
	"context"
	"fmt"
	"os"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/evals"
)

const (
	cpdocsDocumentationPath = "fixtures/acme-stream-api.json"
	cpdocsStreamApiKey      = "resolved-stream-key"
)

// CustomProviderDocsEvalSuite returns the custom streaming provider documentation-lift eval suite.
func CustomProviderDocsEvalSuite() *evals.EvalSuite {
	server := CreateAcmeServer(AcmeServerModeStream)
	var prevEnv string
	var hadPrevEnv bool

	validReq := true
	expected := ProviderRuntimeOutput{
		Result: ProviderRuntimeResult{
			ValidRequestReceived: &validReq,
			Model: &ModelFields{
				Id:        StreamModelId,
				Name:      "Acme Stream Chat",
				Provider:  StreamProviderId,
				Reasoning: false,
				Input:     []string{"text"},
				Cost: ModelFieldsCost{
					Input:      0,
					Output:     0,
					CacheRead:  0,
					CacheWrite: 0,
				},
				ContextWindow: 16384,
				MaxTokens:     2048,
			},
			Response: &ProviderRuntimeResponse{
				Text:         StreamProbeResponse,
				StopReason:   "stop",
				InputTokens:  4,
				OutputTokens: 3,
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
		"Add custom streaming provider",
		evals.DescribeEvalOptions[evals.PiCodingAgentInput, ProviderRuntimeOutput]{
			File:    "evals/suites/custom_provider_docs_eval.go",
			Project: "docs",
			HarnessFactory: func(runOpts evals.SuiteRunOptions) (evals.Harness[evals.PiCodingAgentInput, ProviderRuntimeOutput], error) {
				maxTokens := 32
				opts := evals.PiCodingAgentHarnessOptions[ProviderRuntimeOutput]{
					Model:             runOpts.Model,
					SetupModelRuntime: runOpts.SetupModelRuntime,
					WorkspaceFiles:    map[string]string{cpdocsDocumentationPath: StreamApiDocumentation},
					Output: func(args evals.PiOutputArgs) (ProviderRuntimeOutput, error) {
						return InspectProvider(args.Ctx, args.Session.ModelRuntime(), ProviderScenario{
							ProviderId: StreamProviderId,
							ModelId:    StreamModelId,
							CreateContext: func() *ai.Context {
								prompt := StreamProbePrompt
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
											Env: ai.ProviderEnv{"ACME_STREAM_API_KEY": cpdocsStreamApiKey},
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
				prevEnv, hadPrevEnv = os.LookupEnv("ACME_STREAM_API_KEY")
				if err := os.Setenv("ACME_STREAM_API_KEY", cpdocsStreamApiKey); err != nil {
					return err
				}
				return server.Start()
			})
			it.BeforeEach(func(_ context.Context) error {
				server.Reset()
				return nil
			})
			it.AfterAll(func(_ context.Context) error {
				if hadPrevEnv {
					_ = os.Setenv("ACME_STREAM_API_KEY", prevEnv)
				} else {
					_ = os.Unsetenv("ACME_STREAM_API_KEY")
				}
				return server.Stop()
			})
			it.It("implements a provider from an API fixture", func(rc evals.EvalRunContext[evals.PiCodingAgentInput, ProviderRuntimeOutput]) error {
				_, err := rc.Run(evals.StepsInput(
					evals.PiInputStep{
						Type: evals.PiInputStepPrompt,
						Content: fmt.Sprintf(
							"Configure this running Pi installation with Acme Stream as a provider. Do not create a project package or modify project source. Its provider ID is %s, its API is at %s, and its API documentation is in ./%s. Read its credential from the ACME_STREAM_API_KEY environment variable.\n\nIt offers one model, %s, shown as “Acme Stream Chat”. The model accepts text, does not support reasoning, has a 16,384-token context window and a 2,048-token maximum output, and has no usage cost.",
							StreamProviderId,
							server.Origin(),
							cpdocsDocumentationPath,
							StreamModelId,
						),
					},
					evals.PiInputStep{Type: evals.PiInputStepReload},
				))
				return err
			})
		},
	)
}
