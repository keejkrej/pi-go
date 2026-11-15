// Unit and offline end-to-end integration tests for ported evaluation suites.

package suites_test

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/evals"
	"github.com/keejkrej/pi-go/evals/suites"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func tsuitesSetupFauxSuite(t *testing.T, steps ...providers.FauxResponseStep) (*providers.FauxProviderHandle, evals.SuiteRunOptions) {
	t.Helper()
	faux, err := providers.FauxProvider(&providers.RegisterFauxProviderOptions{
		Models: []providers.FauxModelDefinition{{Id: "faux-eval-model"}},
	})
	if err != nil {
		t.Fatalf("FauxProvider: %v", err)
	}
	if len(steps) > 0 {
		faux.SetResponses(steps)
	}
	model := faux.Models[0]
	return faux, evals.SuiteRunOptions{
		Variant: evals.DocumentationVariantWithDocs,
		Model: &evals.PiCodingAgentModelSelection{
			Provider: string(model.Provider),
			Id:       model.Id,
		},
		SetupModelRuntime: func(_ context.Context, rt *core.ModelRuntime) error {
			return rt.RegisterNativeProvider(faux.Provider)
		},
	}
}

func tsuitesAssertScoreOne(t *testing.T, res evals.VitestAssertionResult) {
	t.Helper()
	if res.Status != "passed" {
		t.Fatalf("status = %q, want passed (failures=%v)", res.Status, res.FailureMessages)
	}
	if res.Meta == nil || res.Meta.Eval == nil || res.Meta.Eval.AvgScore == nil {
		t.Fatalf("expected scored eval metadata, got %+v", res.Meta)
	}
	if *res.Meta.Eval.AvgScore != 1.0 {
		encoded, _ := jsonx.Stringify(res.Meta.Eval.Scores)
		t.Fatalf("avgScore = %v, want 1.0 (scores=%s)", *res.Meta.Eval.AvgScore, encoded)
	}
}

func TestSuitesDiscovery(t *testing.T) {
	all := suites.AllSuites()
	if len(all) != 7 {
		t.Fatalf("len(AllSuites()) = %d, want 7", len(all))
	}
	byProject := make(map[string]int)
	for _, s := range all {
		cases := s.Describe()
		if len(cases) == 0 {
			t.Fatalf("suite %q discovered 0 cases", s.EvalSet)
		}
		byProject[s.Project] += len(cases)
	}
	if byProject["host"] != 1 {
		t.Fatalf("host cases = %d, want 1", byProject["host"])
	}
	if byProject["docs"] != 5 {
		t.Fatalf("docs cases = %d, want 5", byProject["docs"])
	}
	if byProject["audit"] < 10 {
		t.Fatalf("audit cases = %d, want >= 10 markdown doc pages", byProject["audit"])
	}
}

func TestSuitesOfflineExecution(t *testing.T) {
	t.Run("smoke_eval", func(t *testing.T) {
		suite := suites.SmokeEvalSuite()
		cases := suite.Describe()
		_, runOpts := tsuitesSetupFauxSuite(t, providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(providers.FauxAssistantText("Paris"), nil),
		})
		res, err := suite.RunCase(context.Background(), cases[0].CaseId, runOpts)
		if err != nil {
			t.Fatalf("SmokeEvalSuite failed: %v", err)
		}
		if res.Status != "passed" {
			t.Fatalf("status = %q, failures = %v", res.Status, res.FailureMessages)
		}
	})

	t.Run("models_docs_eval", func(t *testing.T) {
		suite := suites.ModelsDocsEvalSuite()
		cases := suite.Describe()
		modelsContent, err := jsonx.MarshalIndent(jsonx.ObjectOf(
			"providers", jsonx.ObjectOf(
				"openai", jsonx.ObjectOf(
					"models", []any{
						jsonx.ObjectOf(
							"id", "fixture-chat",
							"name", "Fixture Chat",
							"reasoning", true,
							"input", []string{"text"},
							"cost", jsonx.ObjectOf("input", 0, "output", 0, "cacheRead", 0, "cacheWrite", 0),
							"contextWindow", 32768,
							"maxTokens", 4096,
						),
					},
				),
			),
		), "  ")
		if err != nil {
			t.Fatalf("MarshalIndent failed: %v", err)
		}
		stopToolUse := ai.StopReasonToolUse
		_, runOpts := tsuitesSetupFauxSuite(t,
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantBlock{
						Block: providers.FauxToolCall("write", jsonx.ObjectOf(
							"path", "~/.pi/agent/models.json",
							"content", string(modelsContent)+"\n",
						), nil),
					},
					&providers.FauxAssistantMessageOptions{StopReason: &stopToolUse},
				),
			},
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText("Configured openai/fixture-chat."), nil),
			},
		)
		res, err := suite.RunCase(context.Background(), cases[0].CaseId, runOpts)
		if err != nil {
			t.Fatalf("ModelsDocsEvalSuite failed: %v", err)
		}
		tsuitesAssertScoreOne(t, res)
	})

	t.Run("openai_provider_docs_eval", func(t *testing.T) {
		suite := suites.OpenaiProviderDocsEvalSuite()
		cases := suite.Describe()
		urlRe := regexp.MustCompile(`http://127\.0\.0\.1:\d+/v1`)
		stopToolUse := ai.StopReasonToolUse
		_, runOpts := tsuitesSetupFauxSuite(t,
			providers.FauxResponseFactory(func(_ context.Context, transcript *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
				promptText := ""
				for _, m := range transcript.Messages {
					if um, ok := m.(*ai.UserMessage); ok && um != nil {
						promptText += ai.ContentText(um.Content) + "\n"
					}
				}
				baseUrl := urlRe.FindString(promptText)
				if baseUrl == "" {
					return nil, fmt.Errorf("could not extract Acme BaseUrl from prompt: %q", promptText)
				}
				modelsContent, _ := jsonx.MarshalIndent(jsonx.ObjectOf(
					"providers", jsonx.ObjectOf(
						suites.OpenaiProviderId, jsonx.ObjectOf(
							"baseUrl", baseUrl,
							"api", "openai-completions",
							"apiKey", "$ACME_API_KEY",
							"models", []any{
								jsonx.ObjectOf(
									"id", suites.OpenaiModelId,
									"name", "Acme Chat",
									"reasoning", false,
									"input", []string{"text"},
									"cost", jsonx.ObjectOf("input", 0, "output", 0, "cacheRead", 0, "cacheWrite", 0),
									"contextWindow", 32768,
									"maxTokens", 4096,
								),
							},
						),
					),
				), "  ")
				return providers.FauxAssistantMessage(
					providers.FauxAssistantBlock{
						Block: providers.FauxToolCall("write", jsonx.ObjectOf(
							"path", "~/.pi/agent/models.json",
							"content", string(modelsContent)+"\n",
						), nil),
					},
					&providers.FauxAssistantMessageOptions{StopReason: &stopToolUse},
				), nil
			}),
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText("Configured acme provider."), nil),
			},
		)
		res, err := suite.RunCase(context.Background(), cases[0].CaseId, runOpts)
		if err != nil {
			t.Fatalf("OpenaiProviderDocsEvalSuite failed: %v", err)
		}
		tsuitesAssertScoreOne(t, res)
	})

	t.Run("custom_provider_docs_eval", func(t *testing.T) {
		suite := suites.CustomProviderDocsEvalSuite()
		cases := suite.Describe()
		originRe := regexp.MustCompile(`http://127\.0\.0\.1:\d+`)
		stopToolUse := ai.StopReasonToolUse
		_, runOpts := tsuitesSetupFauxSuite(t,
			providers.FauxResponseFactory(func(_ context.Context, transcript *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
				promptText := ""
				for _, m := range transcript.Messages {
					if um, ok := m.(*ai.UserMessage); ok && um != nil {
						promptText += ai.ContentText(um.Content) + "\n"
					}
				}
				origin := originRe.FindString(promptText)
				if origin == "" {
					return nil, fmt.Errorf("could not extract Acme Stream origin from prompt: %q", promptText)
				}
				extSrc := fmt.Sprintf(`import { createAssistantMessageEventStream } from "@earendil-works/pi-ai";

export default function (pi: any) {
  pi.registerProvider(%q, {
    baseUrl: %q,
    apiKey: "$ACME_STREAM_API_KEY",
    api: "acme-ndjson",
    models: [
      {
        id: %q,
        name: "Acme Stream Chat",
        reasoning: false,
        input: ["text"],
        cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
        contextWindow: 16384,
        maxTokens: 2048,
      },
    ],
    streamSimple(model: any, context: any, options: any) {
      const stream = createAssistantMessageEventStream();
      (async () => {
        try {
          const messages = (context.messages || []).map((m: any) => ({
            role: m.role,
            content: typeof m.content === "string" ? m.content : (m.content?.[0]?.text || ""),
          }));
          const resp = await fetch(%q + "/generate", {
            method: "POST",
            headers: {
              "content-type": "application/json",
              "x-acme-key": options?.apiKey || process.env.ACME_STREAM_API_KEY || "",
            },
            body: JSON.stringify({ model: model.id, messages, stream: true }),
          });
          if (!resp.ok) {
            throw new Error("HTTP " + resp.status + ": " + (await resp.text()));
          }
          const raw = await resp.text();
          let text = "";
          let inputTokens = 0;
          let outputTokens = 0;
          for (const line of raw.split("\n")) {
            if (!line.trim()) continue;
            const ev = JSON.parse(line);
            if (ev.type === "text_delta") text += ev.text || "";
            if (ev.type === "usage") {
              inputTokens = ev.input_tokens || 0;
              outputTokens = ev.output_tokens || 0;
            }
          }
          const finalMsg: any = {
            role: "assistant",
            content: [{ type: "text", text }],
            api: model.api,
            provider: model.provider,
            model: model.id,
            usage: {
              input: inputTokens,
              output: outputTokens,
              cacheRead: 0,
              cacheWrite: 0,
              totalTokens: inputTokens + outputTokens,
              cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 },
            },
            stopReason: "stop",
            timestamp: Date.now(),
          };
          stream.push({ type: "done", reason: "stop", message: finalMsg });
          stream.end(finalMsg);
        } catch (err: any) {
          const errMsg: any = {
            role: "assistant",
            content: [{ type: "text", text: "" }],
            api: model.api,
            provider: model.provider,
            model: model.id,
            usage: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } },
            stopReason: "error",
            errorMessage: String(err?.message || err),
            timestamp: Date.now(),
          };
          stream.push({ type: "error", reason: "error", error: errMsg });
          stream.end(errMsg);
        }
      })();
      return stream;
    },
  });
}
`, suites.StreamProviderId, origin, suites.StreamModelId, origin)
				return providers.FauxAssistantMessage(
					providers.FauxAssistantBlock{
						Block: providers.FauxToolCall("write", jsonx.ObjectOf(
							"path", "~/.pi/agent/extensions/acme-stream.ts",
							"content", extSrc,
						), nil),
					},
					&providers.FauxAssistantMessageOptions{StopReason: &stopToolUse},
				), nil
			}),
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText("Configured acme-stream extension."), nil),
			},
		)
		res, err := suite.RunCase(context.Background(), cases[0].CaseId, runOpts)
		if err != nil {
			t.Fatalf("CustomProviderDocsEvalSuite failed: %v", err)
		}
		tsuitesAssertScoreOne(t, res)
	})

	t.Run("extensions_docs_eval", func(t *testing.T) {
		suite := suites.ExtensionsDocsEvalSuite()
		cases := suite.Describe()
		stopToolUse := ai.StopReasonToolUse
		helloExt := `import { Type } from "@sinclair/typebox";

export default function (pi: any) {
  pi.registerTool({
    name: "hello",
    label: "Hello",
    description: "Greet a person by name",
    parameters: Type.Object({ name: Type.String() }),
    async execute(_id: string, params: any) {
      return { content: [{ type: "text", text: "Hello, " + params.name + "!" }] };
    },
  });
}
`
		_, runOpts := tsuitesSetupFauxSuite(t,
			// Step 1 turn 1: write extension
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantBlock{
						Block: providers.FauxToolCall("write", jsonx.ObjectOf(
							"path", "~/.pi/agent/extensions/hello.ts",
							"content", helloExt,
						), nil),
					},
					&providers.FauxAssistantMessageOptions{StopReason: &stopToolUse},
				),
			},
			// Step 1 turn 2: confirm setup
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText("Created hello extension."), nil),
			},
			// Step 2 (after reload): call hello tool
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantBlock{
						Block: providers.FauxToolCall("hello", jsonx.ObjectOf("name", "Bob"), nil),
					},
					&providers.FauxAssistantMessageOptions{StopReason: &stopToolUse},
				),
			},
			// Step 2 final response
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText("Hello, Bob!"), nil),
			},
		)
		res, err := suite.RunCase(context.Background(), cases[0].CaseId, runOpts)
		if err != nil {
			t.Fatalf("ExtensionsDocsEvalSuite failed: %v", err)
		}
		tsuitesAssertScoreOne(t, res)
	})

	t.Run("tui_docs_eval", func(t *testing.T) {
		suite := suites.TuiDocsEvalSuite()
		cases := suite.Describe()
		stopToolUse := ai.StopReasonToolUse
		footerExt := `import { truncateToWidth, visibleWidth } from "@earendil-works/pi-tui";

export default function (pi: any) {
  pi.on("session_start", (_event: any, ctx: any) => {
    if (!ctx?.hasUI) return;
    ctx.ui.setFooter((tui: any, theme: any) => ({
      invalidate() {},
      render(width: number): string[] {
        const usage = ctx.getContextUsage();
        const pct = Math.round(usage?.percent ?? 0);
        const capped = Math.max(0, Math.min(100, pct));
        const filled = Math.max(0, Math.min(10, Math.round((usage?.percent ?? 0) / 10)));
        const bar = "█".repeat(filled) + "░".repeat(10 - filled) + " " + capped + "%";
        const cwd = ctx.sessionManager?.getCwd?.() || "";
        const left = theme.fg("dim", "↑106k ↓5.8k R606k $1.008 " + bar);
        const right = theme.fg("dim", ctx.model?.id || "");
        const gap = Math.max(1, width - visibleWidth(left) - visibleWidth(right));
        return [
          theme.fg("dim", cwd),
          truncateToWidth(left + " ".repeat(gap) + right, width),
        ];
      },
    }));
  });
}
`
		_, runOpts := tsuitesSetupFauxSuite(t,
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantBlock{
						Block: providers.FauxToolCall("write", jsonx.ObjectOf(
							"path", "~/.pi/agent/extensions/context-footer.ts",
							"content", footerExt,
						), nil),
					},
					&providers.FauxAssistantMessageOptions{StopReason: &stopToolUse},
				),
			},
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText("Installed custom context footer."), nil),
			},
		)
		res, err := suite.RunCase(context.Background(), cases[0].CaseId, runOpts)
		if err != nil {
			t.Fatalf("TuiDocsEvalSuite failed: %v", err)
		}
		tsuitesAssertScoreOne(t, res)
	})

	t.Run("documentation_audit_eval", func(t *testing.T) {
		suite := suites.DocumentationAuditEvalSuite()
		cases := suite.Describe()
		_, repoRoot, err := evals.DockerResolveRoots()
		if err != nil {
			t.Fatalf("DockerResolveRoots failed: %v", err)
		}
		docRe := regexp.MustCompile(`Documentation page:\s*(\S+)`)
		implPath := filepath.Join(repoRoot, "codingagent", "core", "settings_manager.go")
		stopToolUse := ai.StopReasonToolUse
		_, runOpts := tsuitesSetupFauxSuite(t,
			providers.FauxResponseFactory(func(_ context.Context, transcript *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
				promptText := ""
				for _, m := range transcript.Messages {
					if um, ok := m.(*ai.UserMessage); ok && um != nil {
						promptText += ai.ContentText(um.Content) + "\n"
					}
				}
				m := docRe.FindStringSubmatch(promptText)
				if len(m) < 2 {
					return nil, fmt.Errorf("missing Documentation page in prompt")
				}
				docPath := strings.TrimSpace(m[1])
				return providers.FauxAssistantMessage(
					providers.FauxAssistantBlocks{
						providers.FauxToolCall("read", jsonx.ObjectOf("path", docPath, "limit", 10), nil),
						providers.FauxToolCall("read", jsonx.ObjectOf("path", implPath, "limit", 10), nil),
					},
					&providers.FauxAssistantMessageOptions{StopReason: &stopToolUse},
				), nil
			}),
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantBlock{
						Block: providers.FauxToolCall("submit_documentation_audit", jsonx.ObjectOf(
							"verdict", "match",
							"explanation", "Verified documentation claims match Go implementation.",
						), nil),
					},
					&providers.FauxAssistantMessageOptions{StopReason: &stopToolUse},
				),
			},
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText(""), nil),
			},
		)
		res, err := suite.RunCase(context.Background(), cases[0].CaseId, runOpts)
		if err != nil {
			t.Fatalf("DocumentationAuditEvalSuite failed: %v", err)
		}
		if res.Status != "passed" {
			t.Fatalf("status = %q, failures = %v", res.Status, res.FailureMessages)
		}
	})
}
