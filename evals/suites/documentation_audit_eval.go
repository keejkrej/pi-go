// Ported from packages/evals/evals/documentation-audit.eval.ts (pi v1.0.0).

package suites

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/evals"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

const docauditToolName = "submit_documentation_audit"

func docauditSubmitTool() core.ToolDefinition {
	snippet := "Submit the final documentation audit as validated structured data"
	props := omap.NewMap[string, *typebox.Schema]()
	props.Set("verdict", typebox.Union([]*typebox.Schema{
		typebox.Literal("match"),
		typebox.Literal("mismatch"),
		typebox.Literal("inconclusive"),
	}))
	props.Set("explanation", typebox.String(jsonx.ObjectOf("minLength", 1, "maxLength", 2000)))
	params := typebox.Object(props, jsonx.ObjectOf("additionalProperties", false))

	return core.ToolDefinition{
		Name:          docauditToolName,
		Label:         "Submit documentation audit",
		Description:   "Submit the final verdict after completing the documentation investigation.",
		PromptSnippet: &snippet,
		Parameters:    params,
		ConstrainedSampling: ai.ToolConstrainedSamplingValue{
			V: &ai.ConstrainedSamplingJsonSchema{
				Type:   "json_schema",
				Strict: ai.ConstrainedSamplingStrictPrefer,
			},
		},
		Execute: func(_ context.Context, _ string, parsedParams *jsonx.Object, _ agent.AgentToolUpdateCallback, _ core.ExtensionToolContext) (agent.AgentToolResult, error) {
			terminate := true
			return agent.AgentToolResult{
				Content:   ai.ContentList{&ai.TextContent{Type: "text", Text: "Documentation audit submitted."}},
				Details:   parsedParams,
				Terminate: &terminate,
			}, nil
		},
	}
}

func docauditResolvePaths() (repositoryRoot string, docsRoot string, sourceRoot string, pages []string) {
	_, repoRoot, err := evals.DockerResolveRoots()
	if err != nil {
		return ".", "codingagent/utils/assets/package/docs", "codingagent", nil
	}
	docsDir := filepath.Join(repoRoot, "codingagent", "utils", "assets", "package", "docs")
	srcDir := filepath.Join(repoRoot, "codingagent")

	_ = filepath.WalkDir(docsDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d == nil || d.IsDir() {
			return nil
		}
		if strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			rel, err := filepath.Rel(docsDir, path)
			if err == nil {
				pages = append(pages, filepath.ToSlash(rel))
			}
		}
		return nil
	})
	sort.Strings(pages)
	return repoRoot, docsDir, srcDir, pages
}

func docauditPathWithinRoot(root string, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	if err != nil || rel == "" || rel == "." {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// DocumentationAuditEvalSuite returns the per-page documentation audit suite ("Audit documentation against implementation").
func DocumentationAuditEvalSuite() *evals.EvalSuite {
	repositoryRoot, docsRoot, sourceRoot, pages := docauditResolvePaths()
	submitAudit := docauditSubmitTool()

	return evals.DescribeEval(
		"Audit documentation against implementation",
		evals.DescribeEvalOptions[evals.PiCodingAgentInput, string]{
			File:    "evals/suites/documentation_audit_eval.go",
			Project: "audit",
			HarnessFactory: func(runOpts evals.SuiteRunOptions) (evals.Harness[evals.PiCodingAgentInput, string], error) {
				opts := evals.PiCodingAgentHarnessOptions[string]{
					Name:              "documentation-page-audit",
					Model:             runOpts.Model,
					ToolNames:         []string{"read", "grep", "find", "ls", docauditToolName},
					CustomTools:       []core.ToolDefinition{submitAudit},
					SetupModelRuntime: runOpts.SetupModelRuntime,
				}
				return evals.CreatePiCodingAgentHarness(opts), nil
			},
		},
		func(it *evals.EvalCaseBuilder[evals.PiCodingAgentInput, string]) {
			for _, page := range pages {
				relPage := page
				documentationPath := filepath.Join(docsRoot, filepath.FromSlash(relPage))
				caseName := relPage + " matches the implementation"
				it.It(caseName, func(rc evals.EvalRunContext[evals.PiCodingAgentInput, string]) error {
					prompt := fmt.Sprintf(`Audit this Pi documentation page against the repository implementation.

Documentation page: %s
Repository root: %s

Read the complete page and the relevant implementation. Report a mismatch only for a clear, user-visible contradiction between an explicit documentation claim and actual runtime behavior. Follow the runtime path; names, comments, types, isolated helpers, and tests are not sufficient evidence by themselves.

Missing internal detail, ambiguous wording, hypothetical misuse, and undocumented edge cases are not mismatches. If the evidence is not decisive, report inconclusive. Otherwise report match.

For a mismatch, quote the claim, cite the implementation path and symbol, and state the concrete behavior a user would observe.

Call %s exactly once as your final action. Do not return prose.`, documentationPath, repositoryRoot, docauditToolName)

					res, err := rc.Run(evals.PromptInput(prompt))
					if err != nil {
						return err
					}

					calls := evals.ToolCalls(res)
					var readPaths []string
					readDoc := false
					readSource := false
					var auditCalls []evals.TranscriptToolCall
					for _, call := range calls {
						if call.Name == "read" && call.Status == "ok" && call.Arguments != nil {
							if p, ok := call.Arguments.GetString("path"); ok && p != "" {
								resolved := filepath.Clean(p)
								if abs, err := filepath.Abs(resolved); err == nil {
									resolved = abs
								}
								readPaths = append(readPaths, resolved)
								if resolved == documentationPath {
									readDoc = true
								}
								if docauditPathWithinRoot(sourceRoot, resolved) {
									readSource = true
								}
							}
						}
						if call.Name == docauditToolName {
							auditCalls = append(auditCalls, call)
						}
					}
					if !readDoc {
						return fmt.Errorf("expected read of documentation page %q, read paths: %v", documentationPath, readPaths)
					}
					if !readSource {
						return fmt.Errorf("expected read inside implementation root %q, read paths: %v", sourceRoot, readPaths)
					}
					if len(auditCalls) != 1 {
						return fmt.Errorf("expected exactly 1 call to %s, got %d", docauditToolName, len(auditCalls))
					}
					auditCall := auditCalls[0]
					if auditCall.Status != "ok" {
						return fmt.Errorf("%s status = %q, want ok (error=%q)", docauditToolName, auditCall.Status, auditCall.Error)
					}
					verdict := ""
					explanation := ""
					if auditCall.Arguments != nil {
						verdict, _ = auditCall.Arguments.GetString("verdict")
						explanation, _ = auditCall.Arguments.GetString("explanation")
					}
					if verdict != "match" {
						return fmt.Errorf("audit verdict = %q, want \"match\" (explanation: %s)", verdict, explanation)
					}
					return nil
				})
			}
		},
	)
}
