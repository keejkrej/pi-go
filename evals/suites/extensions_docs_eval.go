// Ported from packages/evals/evals/extensions.docs.eval.ts (pi v1.0.0).

package suites

import (
	"slices"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/evals"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

const (
	extdocsToolName   = "hello"
	extdocsToolResult = "Hello, Bob!"
)

// ExtensionsDocsOutput captures the response, load errors, and matching tool result inspected after running extension evals.
type ExtensionsDocsOutput struct {
	Response        string                    `json:"response"`
	ExtensionErrors []core.ExtensionLoadError `json:"extensionErrors"`
	ToolResult      *string                   `json:"toolResult"`
}

// ExtensionsDocsEvalSuite returns the extensions documentation-lift eval suite ("Create and use a tool extension").
func ExtensionsDocsEvalSuite() *evals.EvalSuite {
	expectedResult := extdocsToolResult
	structuredJudge := evals.StructuredOutputJudge[evals.PiCodingAgentInput, ExtensionsDocsOutput, ExtensionsDocsOutput](
		evals.StructuredOutputJudgeOptions[ExtensionsDocsOutput]{
			Expected: ExtensionsDocsOutput{
				Response:        extdocsToolResult,
				ExtensionErrors: []core.ExtensionLoadError{},
				ToolResult:      &expectedResult,
			},
			Match:       "strict",
			AllowExtras: false,
		},
	)
	toolJudge := evals.ToolCallJudge[evals.PiCodingAgentInput, ExtensionsDocsOutput](
		evals.ToolCallJudgeOptions{
			ExpectedTools: []evals.ExpectedToolCall{
				{
					Name:      extdocsToolName,
					Arguments: jsonx.ObjectOf("name", "Bob"),
				},
			},
		},
	)

	tools := append(slices.Clone(evals.DocumentationEvalTools), extdocsToolName)

	return evals.DescribeEval(
		"Create and use a tool extension",
		evals.DescribeEvalOptions[evals.PiCodingAgentInput, ExtensionsDocsOutput]{
			File:    "evals/suites/extensions_docs_eval.go",
			Project: "docs",
			HarnessFactory: func(runOpts evals.SuiteRunOptions) (evals.Harness[evals.PiCodingAgentInput, ExtensionsDocsOutput], error) {
				opts := evals.PiCodingAgentHarnessOptions[ExtensionsDocsOutput]{
					Model:             runOpts.Model,
					SetupModelRuntime: runOpts.SetupModelRuntime,
					ToolNames:         slices.Clone(tools),
					Output: func(args evals.PiOutputArgs) (ExtensionsDocsOutput, error) {
						extensions := args.Session.ResourceLoader().GetExtensions()
						errs := extensions.Errors
						if errs == nil {
							errs = []core.ExtensionLoadError{}
						}
						var toolResultText *string
						messages := args.Session.Messages()
						for i := len(messages) - 1; i >= 0; i-- {
							tr, ok := messages[i].(*ai.ToolResultMessage)
							if !ok || tr == nil {
								continue
							}
							if tr.ToolName == extdocsToolName && !tr.IsError {
								txt := ai.ContentText(tr.Content)
								toolResultText = &txt
								break
							}
						}
						return ExtensionsDocsOutput{
							Response:        args.Response,
							ExtensionErrors: errs,
							ToolResult:      toolResultText,
						}, nil
					},
				}
				if runOpts.Variant != "" {
					return evals.CreatePiDocumentationEvalHarnessForTest(runOpts.Variant, opts)
				}
				return evals.CreatePiDocumentationEvalHarness(opts)
			},
			Judges:         []evals.Judge[evals.PiCodingAgentInput, ExtensionsDocsOutput]{structuredJudge, toolJudge},
			JudgeThreshold: nil,
		},
		func(it *evals.EvalCaseBuilder[evals.PiCodingAgentInput, ExtensionsDocsOutput]) {
			it.It("creates, reloads, and invokes the extension", func(rc evals.EvalRunContext[evals.PiCodingAgentInput, ExtensionsDocsOutput]) error {
				_, err := rc.Run(evals.StepsInput(
					evals.PiInputStep{
						Type:    evals.PiInputStepPrompt,
						Content: "Configure this running Pi installation with an extension containing a hello tool that takes a name and returns a greeting. Do not create project source. For example, passing Bob should return `Hello, Bob!`.",
					},
					evals.PiInputStep{Type: evals.PiInputStepReload},
					evals.PiInputStep{
						Type:    evals.PiInputStepPrompt,
						Content: "Use the hello tool to greet Bob. Respond with exactly the tool's greeting and nothing else.",
					},
				))
				return err
			})
		},
	)
}
