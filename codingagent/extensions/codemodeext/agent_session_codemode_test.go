// Ported from packages/coding-agent/test/suite/agent-session-codemode.test.ts (pi v1.5.2).

package codemodeext_test

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/extensions/codemodeext"
	"github.com/keejkrej/pi-go/codingagent/extensions/toolsearchext"
	"github.com/keejkrej/pi-go/codingagent/internal/testkit"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

const (
	tascPngBase64A = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII="
	tascPngBase64B = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
)

func tascBoolPtr(v bool) *bool    { return &v }
func tascStrPtr(v string) *string { return &v }

type tascHarnessOptions struct {
	Cwd                    string
	Settings               *core.Settings
	InitialActiveToolNames []string
	Extensions             []core.InlineExtension
	OmitDefaultCodemode    bool
	SessionManager         *core.SessionManager
	Tools                  []*agent.AgentTool
}

func tascCreateHarness(t *testing.T, opts *tascHarnessOptions) *testkit.SuiteHarness {
	t.Helper()
	ctx := context.Background()
	cwd := ""
	if opts != nil {
		cwd = opts.Cwd
	}
	if cwd == "" {
		cwd = t.TempDir()
	}

	var factories []core.InlineExtension
	if opts == nil || !opts.OmitDefaultCodemode {
		factories = append(factories, core.InlineExtension{
			Name:    "codemode",
			Factory: codemodeext.Extension,
		})
	}
	if opts != nil && len(opts.Extensions) > 0 {
		factories = append(factories, opts.Extensions...)
	}

	extRes := core.LoadExtensionsWithOptions(ctx, nil, &core.LoadExtensionsOptions{
		Cwd:       cwd,
		Factories: factories,
	})
	if len(extRes.Errors) != 0 {
		t.Fatalf("LoadExtensionsWithOptions errors: %+v", extRes.Errors)
	}
	rl := testkit.CreateTestResourceLoader(&testkit.CreateTestResourceLoaderOptions{
		ExtensionsResult: &extRes,
	})

	harnessOpts := &testkit.SuiteHarnessOptions{
		Cwd:            cwd,
		ResourceLoader: rl,
	}
	if opts != nil {
		harnessOpts.Settings = opts.Settings
		harnessOpts.InitialActiveToolNames = opts.InitialActiveToolNames
		harnessOpts.SessionManager = opts.SessionManager
		harnessOpts.Tools = opts.Tools
	}
	h, err := testkit.CreateSuiteHarness(ctx, harnessOpts)
	if err != nil {
		t.Fatalf("CreateSuiteHarness failed: %v", err)
	}
	t.Cleanup(h.Cleanup)
	return h
}

func tascObjectSchema(pairs ...tascSchemaProp) *typebox.Schema {
	props := omap.NewMap[string, *typebox.Schema]()
	for _, p := range pairs {
		props.Set(p.name, p.schema)
	}
	return typebox.Object(props)
}

type tascSchemaProp struct {
	name   string
	schema *typebox.Schema
}

func tascCodemodeStep(code string) providers.FauxResponseStep {
	stopTool := ai.StopReasonToolUse
	return &providers.FauxResponseMessage{
		Message: providers.FauxAssistantMessage(
			providers.FauxAssistantBlock{
				Block: providers.FauxToolCall("codemode", jsonx.ObjectOf("code", code), nil),
			},
			&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
		),
	}
}

func tascDoneStep(text string) providers.FauxResponseStep {
	return &providers.FauxResponseMessage{
		Message: providers.FauxAssistantMessage(providers.FauxAssistantText(text), nil),
	}
}

func tascRunCodemode(t *testing.T, h *testkit.SuiteHarness, code string) *ai.ToolResultMessage {
	t.Helper()
	h.SetResponses([]providers.FauxResponseStep{
		tascCodemodeStep(code),
		tascDoneStep("done"),
	})
	if err := h.Session.Prompt(context.Background(), "run"); err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}
	tr, err := testkit.GetToolResult(h, codemodeext.CodemodeToolName)
	if err != nil {
		t.Fatalf("GetToolResult(%q): %v", codemodeext.CodemodeToolName, err)
	}
	return tr
}

func tascResultText(tr *ai.ToolResultMessage) string {
	if tr == nil {
		return ""
	}
	var parts []string
	for _, item := range tr.Content {
		if tc, ok := item.(*ai.TextContent); ok && tc != nil {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func tascExtractSavedPath(label string) string {
	const prefix = "[Image saved to "
	idx := strings.Index(label, prefix)
	if idx < 0 {
		return ""
	}
	rest := label[idx+len(prefix):]
	end := strings.Index(rest, " (")
	if end < 0 {
		return ""
	}
	return rest[:end]
}

func tascMakeObj(pairs ...any) *jsonx.Object {
	return jsonx.ObjectOf(pairs...)
}

func tascCodemodeExtension(opts *codemodeext.CodemodeExtensionOptions) core.InlineExtension {
	return core.InlineExtension{
		Name:    "codemode",
		Factory: codemodeext.CreateCodemodeExtension(opts),
	}
}

func TestAgentSessionCodemode(t *testing.T) {
	t.Run("presents callable tools per codemode.mode", func(t *testing.T) {
		hiddenToolExt := core.InlineExtension{
			Name: "hidden-tool",
			Factory: func(pi core.ExtensionAPI) error {
				pi.RegisterTool(core.ToolDefinition{
					Name:        "ping_hidden",
					Label:       "Ping Hidden",
					Description: "Hidden tool callable only through codemode",
					Exposure:    core.ToolExposureCodemode,
					Parameters:  tascObjectSchema(tascSchemaProp{"msg", typebox.Optional(typebox.String())}),
					Execute: func(_ context.Context, _ string, _ *jsonx.Object, _ agent.AgentToolUpdateCallback, _ core.ExtensionToolContext) (agent.AgentToolResult, error) {
						return agent.AgentToolResult{
							Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "pong"}},
						}, nil
					},
				})
				return nil
			},
		}
		h := tascCreateHarness(t, &tascHarnessOptions{
			Extensions: []core.InlineExtension{hiddenToolExt},
		})
		// Initially codemode is inactive; direct tools remain active.
		var sawInitial []string
		stopDone := ai.StopReasonStop
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseFactory(func(_ context.Context, tr *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
				for _, tool := range ai.GetCurrentTools(tr.Messages) {
					sawInitial = append(sawInitial, tool.Name)
				}
				return providers.FauxAssistantMessage(providers.FauxAssistantText("ok"), &providers.FauxAssistantMessageOptions{StopReason: &stopDone}), nil
			}),
		})
		if err := h.Session.Prompt(context.Background(), "check initial"); err != nil {
			t.Fatalf("Prompt failed: %v", err)
		}
		if strings.Contains(strings.Join(sawInitial, ","), "codemode") {
			t.Fatalf("expected codemode inactive initially, saw %v", sawInitial)
		}

		// Enable codemode in default mode "on": direct tools remain declared; codemode lists only non-direct callable tools.
		h.Session.SetActiveToolsByName([]string{"read", "bash", "codemode"})
		var sawModeOn []string
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseFactory(func(_ context.Context, tr *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
				for _, tool := range ai.GetCurrentTools(tr.Messages) {
					sawModeOn = append(sawModeOn, tool.Name)
				}
				return providers.FauxAssistantMessage(providers.FauxAssistantText("ok"), &providers.FauxAssistantMessageOptions{StopReason: &stopDone}), nil
			}),
		})
		if err := h.Session.Prompt(context.Background(), "check mode on"); err != nil {
			t.Fatalf("Prompt failed: %v", err)
		}
		onJoined := strings.Join(sawModeOn, ",")
		if !strings.Contains(onJoined, "codemode") || !strings.Contains(onJoined, "read") || strings.Contains(onJoined, "ping_hidden") {
			t.Fatalf("mode=on tools = %v, want codemode+read declared and ping_hidden hidden", sawModeOn)
		}
		var cmDescOn, readDescOn string
		for _, tool := range h.Session.State().Tools {
			switch tool.Name {
			case codemodeext.CodemodeToolName:
				cmDescOn = tool.Description
			case "read":
				readDescOn = tool.Description
			}
		}
		if !strings.Contains(readDescOn, "Codemode: `tools.read(args)` resolves to") {
			t.Fatalf("mode=on direct tool read should describe tools.read(args), got: %s", readDescOn)
		}
		if strings.Contains(cmDescOn, "### `read`") || !strings.Contains(cmDescOn, "### `ping_hidden`") {
			t.Fatalf("mode=on codemode should list ping_hidden and omit direct read, got:\n%s", cmDescOn)
		}

		// Switch to codemode.mode = "only": direct tools removed from LLM tool declarations while staying script-callable.
		onlyMode := core.CodemodeModeOnly
		h2 := tascCreateHarness(t, &tascHarnessOptions{
			Settings: &core.Settings{
				Codemode: &core.CodemodeSettings{Mode: &onlyMode},
			},
			Extensions: []core.InlineExtension{hiddenToolExt},
		})
		h2.Session.SetActiveToolsByName([]string{"read", "bash", "codemode"})
		var sawModeOnly []string
		var cmDescOnly string
		stopTool := ai.StopReasonToolUse
		h2.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseFactory(func(_ context.Context, tr *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
				for _, tool := range ai.GetCurrentTools(tr.Messages) {
					sawModeOnly = append(sawModeOnly, tool.Name)
					if tool.Name == codemodeext.CodemodeToolName {
						cmDescOnly = tool.Description
					}
				}
				return providers.FauxAssistantMessage(
					providers.FauxAssistantBlock{Block: providers.FauxToolCall("codemode", tascMakeObj("code", "return await tools.ping_hidden({msg:'hi'});"), nil)},
					&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
				), nil
			}),
			tascDoneStep("done"),
		})
		if err := h2.Session.Prompt(context.Background(), "check mode only"); err != nil {
			t.Fatalf("Prompt mode=only failed: %v", err)
		}
		if len(sawModeOnly) != 1 || sawModeOnly[0] != "codemode" {
			t.Fatalf("mode=only declared tools = %v, want only [codemode]", sawModeOnly)
		}
		if !strings.Contains(cmDescOnly, "### `read`") || !strings.Contains(cmDescOnly, "### `ping_hidden`") {
			t.Fatalf("mode=only codemode description should include both read and ping_hidden, got:\n%s", cmDescOnly)
		}
		tr, err := testkit.GetToolResult(h2, codemodeext.CodemodeToolName)
		if err != nil {
			t.Fatalf("GetToolResult failed: %v", err)
		}
		if !strings.Contains(tascResultText(tr), "pong") {
			t.Fatalf("mode=only script result = %q, want pong", tascResultText(tr))
		}
	})

	t.Run("summarizes union and multi-line output types instead of inlining them", func(t *testing.T) {
		ext := core.InlineExtension{
			Name: "complex-outputs",
			Factory: func(pi core.ExtensionAPI) error {
				pi.RegisterTool(core.ToolDefinition{
					Name:        "union_tool",
					Label:       "Union Tool",
					Description: "Returns a multi-branch union",
					Exposure:    core.ToolExposureDirect,
					Parameters:  tascObjectSchema(tascSchemaProp{"q", typebox.String()}),
					OutputSchema: typebox.Union([]*typebox.Schema{
						tascObjectSchema(
							tascSchemaProp{"kind", typebox.Literal("alpha")},
							tascSchemaProp{"items", typebox.Array(typebox.String())},
						),
						tascObjectSchema(
							tascSchemaProp{"kind", typebox.Literal("beta")},
							tascSchemaProp{"count", typebox.Number()},
						),
					}),
					Execute: func(context.Context, string, *jsonx.Object, agent.AgentToolUpdateCallback, core.ExtensionToolContext) (agent.AgentToolResult, error) {
						return agent.AgentToolResult{}, nil
					},
				})
				pi.RegisterTool(core.ToolDefinition{
					Name:        "multiline_tool",
					Label:       "Multiline Tool",
					Description: "Returns a wide multi-line object",
					Exposure:    core.ToolExposureDirect,
					Parameters:  tascObjectSchema(tascSchemaProp{"q", typebox.String()}),
					OutputSchema: typebox.Array(tascObjectSchema(
						tascSchemaProp{"id", typebox.String(jsonx.ObjectOf("description", "Entry identifier"))},
						tascSchemaProp{"size", typebox.Number(jsonx.ObjectOf("description", "Byte size"))},
						tascSchemaProp{"owner", typebox.String(jsonx.ObjectOf("description", "Owner account"))},
					)),
					Execute: func(context.Context, string, *jsonx.Object, agent.AgentToolUpdateCallback, core.ExtensionToolContext) (agent.AgentToolResult, error) {
						return agent.AgentToolResult{}, nil
					},
				})
				return nil
			},
		}
		h := tascCreateHarness(t, &tascHarnessOptions{
			Extensions: []core.InlineExtension{ext},
		})
		h.Session.SetActiveToolsByName([]string{"codemode", "union_tool", "multiline_tool"})
		descs := make(map[string]string)
		stopDone := ai.StopReasonStop
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseFactory(func(_ context.Context, tr *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
				for _, tool := range ai.GetCurrentTools(tr.Messages) {
					descs[tool.Name] = tool.Description
				}
				return providers.FauxAssistantMessage(providers.FauxAssistantText("ok"), &providers.FauxAssistantMessageOptions{StopReason: &stopDone}), nil
			}),
		})
		if err := h.Session.Prompt(context.Background(), "inspect catalog"); err != nil {
			t.Fatalf("Prompt failed: %v", err)
		}
		if !strings.Contains(descs["union_tool"], `{ kind: "alpha", items } | { kind: "beta", count }`) {
			t.Fatalf("expected summarized union_tool fields, got:\n%s", descs["union_tool"])
		}
		if !strings.Contains(descs["multiline_tool"], `a value; `+"`await describeTool(\"multiline_tool\")`"+` shows its type`) {
			t.Fatalf("expected multi-line array output to defer to await describeTool(\"multiline_tool\"), got:\n%s", descs["multiline_tool"])
		}

		// Verify describeTool returns full output schema in codemode.
		tr := tascRunCodemode(t, h, `return [await describeTool("union_tool"), await describeTool("multiline_tool")].join("\n---\n");`)
		out := tascResultText(tr)
		if !strings.Contains(out, "alpha") || !strings.Contains(out, "beta") || !strings.Contains(out, "size") {
			t.Fatalf("expected describeTool to include full fields, got:\n%s", out)
		}
	})

	t.Run("summarizes output fields behind local schema references", func(t *testing.T) {
		nodeProps := omap.NewMap[string, *typebox.Schema]()
		nodeProps.Set("id", typebox.String())
		nodeProps.Set("score", typebox.Number())
		defs := omap.NewMap[string, *typebox.Schema]()
		defs.Set("ResultNode", typebox.Object(nodeProps))
		outSchema := typebox.Object(func() *omap.Map[string, *typebox.Schema] {
			m := omap.NewMap[string, *typebox.Schema]()
			m.Set("node", typebox.Ref("#/$defs/ResultNode"))
			m.Set("cursor", typebox.String())
			m.Set("total", typebox.Number())
			return m
		}(), jsonx.ObjectOf("$defs", defs))

		ext := core.InlineExtension{
			Name: "ref-output",
			Factory: func(pi core.ExtensionAPI) error {
				pi.RegisterTool(core.ToolDefinition{
					Name:         "ref_tool",
					Label:        "Ref Tool",
					Description:  "Uses $defs ref output",
					Exposure:     core.ToolExposureDirect,
					Parameters:   tascObjectSchema(tascSchemaProp{"q", typebox.String()}),
					OutputSchema: outSchema,
					Execute: func(context.Context, string, *jsonx.Object, agent.AgentToolUpdateCallback, core.ExtensionToolContext) (agent.AgentToolResult, error) {
						return agent.AgentToolResult{}, nil
					},
				})
				return nil
			},
		}
		h := tascCreateHarness(t, &tascHarnessOptions{
			Extensions: []core.InlineExtension{ext},
		})
		h.Session.SetActiveToolsByName([]string{"codemode", "ref_tool"})
		var refDesc string
		stopDone := ai.StopReasonStop
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseFactory(func(_ context.Context, tr *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
				for _, tool := range ai.GetCurrentTools(tr.Messages) {
					if tool.Name == "ref_tool" {
						refDesc = tool.Description
					}
				}
				return providers.FauxAssistantMessage(providers.FauxAssistantText("ok"), &providers.FauxAssistantMessageOptions{StopReason: &stopDone}), nil
			}),
		})
		if err := h.Session.Prompt(context.Background(), "check ref summary"); err != nil {
			t.Fatalf("Prompt failed: %v", err)
		}
		if !strings.Contains(refDesc, "node: { id, score }") {
			t.Fatalf("expected ref output summary to follow #/$defs ref and surface node: { id, score }, got:\n%s", refDesc)
		}
	})

	t.Run("shows the guidelines of tools that do not fit the inline budget through describeTool()", func(t *testing.T) {
		ns := &core.ToolNamespace{
			Name:        "github",
			Description: tascStrPtr("GitHub API tools"),
		}
		ext := core.InlineExtension{
			Name: "budget-ns",
			Factory: func(pi core.ExtensionAPI) error {
				pi.RegisterTool(core.ToolDefinition{
					Name:             "github_search",
					Label:            "GitHub Search",
					Description:      "Search repositories and issues on GitHub.",
					PromptGuidelines: []string{"Prefer repo:owner/name qualifiers when searching code."},
					Namespace:        ns,
					Exposure:         core.ToolExposureCodemode,
					Parameters:       tascObjectSchema(tascSchemaProp{"query", typebox.String()}),
					Execute: func(context.Context, string, *jsonx.Object, agent.AgentToolUpdateCallback, core.ExtensionToolContext) (agent.AgentToolResult, error) {
						return agent.AgentToolResult{}, nil
					},
				})
				return nil
			},
		}
		onlyMode := core.CodemodeModeOnly
		zeroBudget := 0.0
		h := tascCreateHarness(t, &tascHarnessOptions{
			Settings: &core.Settings{
				Codemode: &core.CodemodeSettings{Mode: &onlyMode, InlineBudget: &zeroBudget},
			},
			Extensions: []core.InlineExtension{ext},
		})
		h.Session.SetActiveToolsByName([]string{"codemode", "github_search"})
		var cmDesc string
		stopDone := ai.StopReasonStop
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseFactory(func(_ context.Context, tr *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
				for _, tool := range ai.GetCurrentTools(tr.Messages) {
					if tool.Name == codemodeext.CodemodeToolName {
						cmDesc = tool.Description
					}
				}
				return providers.FauxAssistantMessage(providers.FauxAssistantText("ok"), &providers.FauxAssistantMessageOptions{StopReason: &stopDone}), nil
			}),
		})
		if err := h.Session.Prompt(context.Background(), "budget check"); err != nil {
			t.Fatalf("Prompt failed: %v", err)
		}
		if strings.Contains(cmDesc, "Prefer repo:owner/name") || strings.Contains(cmDesc, "### `github_search`") {
			t.Fatalf("zero inline budget should omit guidelines/declarations from inline description, got:\n%s", cmDesc)
		}
		if !strings.Contains(cmDesc, "## github (tools not listed)") {
			t.Fatalf("zero inline budget should mark namespace unlisted, got:\n%s", cmDesc)
		}
		tr := tascRunCodemode(t, h, `return await describeTool("github_search");`)
		if out := tascResultText(tr); !strings.Contains(out, "Prefer repo:owner/name qualifiers") {
			t.Fatalf("describeTool output missing guideline: %s", out)
		}
	})

	t.Run("runs nested calls in parallel and returns only the script result", func(t *testing.T) {
		var inFlight atomic.Int32
		var maxInFlight atomic.Int32
		bothStarted := make(chan struct{})
		var entered atomic.Int32

		ext := core.InlineExtension{
			Name: "parallel-tool",
			Factory: func(pi core.ExtensionAPI) error {
				pi.RegisterTool(core.ToolDefinition{
					Name:        "slow_step",
					Label:       "Slow Step",
					Description: "Waits until concurrent peer arrives",
					Exposure:    core.ToolExposureCodemode,
					Parameters:  tascObjectSchema(tascSchemaProp{"id", typebox.String()}),
					Execute: func(ctx context.Context, _ string, params *jsonx.Object, _ agent.AgentToolUpdateCallback, _ core.ExtensionToolContext) (agent.AgentToolResult, error) {
						cur := inFlight.Add(1)
						for {
							prev := maxInFlight.Load()
							if cur <= prev || maxInFlight.CompareAndSwap(prev, cur) {
								break
							}
						}
						if entered.Add(1) == 2 {
							close(bothStarted)
						}
						select {
						case <-bothStarted:
						case <-time.After(2 * time.Second):
						case <-ctx.Done():
							inFlight.Add(-1)
							return agent.AgentToolResult{}, ctx.Err()
						}
						inFlight.Add(-1)
						idVal, _ := params.Get("id")
						return agent.AgentToolResult{
							Content: ai.ContentList{&ai.TextContent{Type: "text", Text: fmt.Sprintf("step:%v", idVal)}},
						}, nil
					},
				})
				return nil
			},
		}
		h := tascCreateHarness(t, &tascHarnessOptions{Extensions: []core.InlineExtension{ext}})
		h.Session.SetActiveToolsByName([]string{"codemode", "slow_step"})
		tr := tascRunCodemode(t, h, `
			const [a, b] = await Promise.all([
				tools.slow_step({ id: "A" }),
				tools.slow_step({ id: "B" }),
			]);
			return "joined:" + a + "+" + b;
		`)
		if maxInFlight.Load() != 2 {
			t.Fatalf("expected maxInFlight=2, got %d", maxInFlight.Load())
		}
		out := tascResultText(tr)
		if !strings.Contains(out, "joined:step:A+step:B") {
			t.Fatalf("unexpected script result: %q", out)
		}
	})

	t.Run("routes nested calls through extension hooks", func(t *testing.T) {
		var sawToolCall bool
		ext := core.InlineExtension{
			Name: "hook-ext",
			Factory: func(pi core.ExtensionAPI) error {
				pi.RegisterTool(core.ToolDefinition{
					Name:        "guarded_tool",
					Label:       "Guarded",
					Description: "Checked by tool_call hook",
					Exposure:    core.ToolExposureCodemode,
					Parameters:  tascObjectSchema(tascSchemaProp{"action", typebox.String()}),
					Execute: func(_ context.Context, _ string, params *jsonx.Object, _ agent.AgentToolUpdateCallback, _ core.ExtensionToolContext) (agent.AgentToolResult, error) {
						act, _ := params.Get("action")
						return agent.AgentToolResult{
							Content: ai.ContentList{&ai.TextContent{Type: "text", Text: fmt.Sprintf("raw:%v", act)}},
						}, nil
					},
				})
				pi.OnToolCall(func(_ context.Context, ev *core.ToolCallEvent, _ core.ExtensionContext) (*core.ToolCallEventResult, error) {
					if ev.ToolName == "guarded_tool" {
						sawToolCall = true
						if act, _ := ev.Input.Get("action"); act == "deny" {
							reason := "blocked by hook"
							return &core.ToolCallEventResult{Block: tascBoolPtr(true), Reason: &reason}, nil
						}
					}
					return nil, nil
				})
				pi.OnToolResult(func(_ context.Context, ev *core.ToolResultEvent, _ core.ExtensionContext) (*core.ToolResultEventResult, error) {
					if ev.ToolName == "guarded_tool" && !ev.IsError {
						return &core.ToolResultEventResult{
							Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "rewritten-by-hook"}},
						}, nil
					}
					return nil, nil
				})
				return nil
			},
		}
		h := tascCreateHarness(t, &tascHarnessOptions{Extensions: []core.InlineExtension{ext}})
		h.Session.SetActiveToolsByName([]string{"codemode", "guarded_tool"})
		tr := tascRunCodemode(t, h, `
			let deniedMsg = "";
			try {
				await tools.guarded_tool({ action: "deny" });
			} catch (err) {
				deniedMsg = String(err.message || err);
			}
			const allowed = await tools.guarded_tool({ action: "allow" });
			return JSON.stringify({ deniedMsg, allowed });
		`)
		if !sawToolCall {
			t.Fatal("expected tool_call hook to see nested call")
		}
		out := tascResultText(tr)
		if !strings.Contains(out, "blocked by hook") || !strings.Contains(out, "rewritten-by-hook") {
			t.Fatalf("unexpected hook output: %s", out)
		}
	})

	t.Run("adds the usage of nested results to the codemode result", func(t *testing.T) {
		ext := core.InlineExtension{
			Name: "usage-ext",
			Factory: func(pi core.ExtensionAPI) error {
				pi.RegisterTool(core.ToolDefinition{
					Name:        "costly_tool",
					Label:       "Costly",
					Description: "Reports usage",
					Exposure:    core.ToolExposureCodemode,
					Parameters:  tascObjectSchema(tascSchemaProp{"n", typebox.Number()}),
					Execute: func(context.Context, string, *jsonx.Object, agent.AgentToolUpdateCallback, core.ExtensionToolContext) (agent.AgentToolResult, error) {
						return agent.AgentToolResult{
							Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "ok"}},
							Usage: &ai.Usage{
								Input:       100,
								Output:      50,
								TotalTokens: 150,
								Cost:        ai.UsageCost{Input: 0.01, Output: 0.02, Total: 0.03},
							},
						}, nil
					},
				})
				return nil
			},
		}
		h := tascCreateHarness(t, &tascHarnessOptions{Extensions: []core.InlineExtension{ext}})
		h.Session.SetActiveToolsByName([]string{"codemode", "costly_tool"})
		tr := tascRunCodemode(t, h, `await tools.costly_tool({n:1}); await tools.costly_tool({n:2}); return "done";`)
		if tr.Usage == nil || tr.Usage.Input != 200 || tr.Usage.Output != 100 || tr.Usage.TotalTokens != 300 {
			t.Fatalf("unexpected aggregated usage on codemode result: %+v", tr.Usage)
		}
		if tr.Usage.Cost.Total < 0.059 || tr.Usage.Cost.Total > 0.061 {
			t.Fatalf("unexpected aggregated cost total: %v", tr.Usage.Cost.Total)
		}
	})

	t.Run("keeps structured content that tool_result handlers replace along with the content", func(t *testing.T) {
		ext := core.InlineExtension{
			Name: "structured-hook",
			Factory: func(pi core.ExtensionAPI) error {
				pi.RegisterTool(core.ToolDefinition{
					Name:         "struct_tool",
					Label:        "Struct Tool",
					Description:  "Returns structured output",
					Exposure:     core.ToolExposureCodemode,
					Parameters:   tascObjectSchema(),
					OutputSchema: tascObjectSchema(tascSchemaProp{"value", typebox.String()}),
					Execute: func(context.Context, string, *jsonx.Object, agent.AgentToolUpdateCallback, core.ExtensionToolContext) (agent.AgentToolResult, error) {
						return agent.AgentToolResult{
							Content:           ai.ContentList{&ai.TextContent{Type: "text", Text: "original text"}},
							StructuredContent: jsonx.ObjectOf("value", "orig"),
						}, nil
					},
				})
				pi.OnToolResult(func(_ context.Context, ev *core.ToolResultEvent, _ core.ExtensionContext) (*core.ToolResultEventResult, error) {
					if ev.ToolName == "struct_tool" {
						return &core.ToolResultEventResult{
							Content:           ai.ContentList{&ai.TextContent{Type: "text", Text: "replaced text"}},
							StructuredContent: jsonx.ObjectOf("value", "rewritten"),
						}, nil
					}
					return nil, nil
				})
				return nil
			},
		}
		h := tascCreateHarness(t, &tascHarnessOptions{Extensions: []core.InlineExtension{ext}})
		h.Session.SetActiveToolsByName([]string{"codemode", "struct_tool"})
		tr := tascRunCodemode(t, h, `const r = await tools.struct_tool({}); return JSON.stringify(r);`)
		if !strings.Contains(tascResultText(tr), `"value":"rewritten"`) {
			t.Fatalf("expected rewritten structured content in script result, got %s", tascResultText(tr))
		}
	})

	t.Run("attaches only the images the script passes to image(), in output order, each after its saved path", func(t *testing.T) {
		ext := core.InlineExtension{
			Name: "img-tool",
			Factory: func(pi core.ExtensionAPI) error {
				pi.RegisterTool(core.ToolDefinition{
					Name:        "emit_images",
					Label:       "Emit Images",
					Description: "Returns two images",
					Exposure:    core.ToolExposureCodemode,
					Parameters:  tascObjectSchema(),
					OutputSchema: tascObjectSchema(
						tascSchemaProp{"first", tascObjectSchema(tascSchemaProp{"type", typebox.Literal("image")}, tascSchemaProp{"data", typebox.String()}, tascSchemaProp{"mimeType", typebox.String()})},
						tascSchemaProp{"second", tascObjectSchema(tascSchemaProp{"type", typebox.Literal("image")}, tascSchemaProp{"data", typebox.String()}, tascSchemaProp{"mimeType", typebox.String()})},
					),
					Execute: func(context.Context, string, *jsonx.Object, agent.AgentToolUpdateCallback, core.ExtensionToolContext) (agent.AgentToolResult, error) {
						return agent.AgentToolResult{
							Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "two images"}},
							StructuredContent: jsonx.ObjectOf(
								"first", jsonx.ObjectOf("type", "image", "data", tascPngBase64A, "mimeType", "image/png"),
								"second", jsonx.ObjectOf("type", "image", "data", tascPngBase64B, "mimeType", "image/png"),
							),
						}, nil
					},
				})
				return nil
			},
		}
		h := tascCreateHarness(t, &tascHarnessOptions{Extensions: []core.InlineExtension{ext}})
		h.Session.SetActiveToolsByName([]string{"codemode", "emit_images"})
		tr := tascRunCodemode(t, h, `
			const out = await tools.emit_images({});
			image(out.second);
			text("between");
			image(out.first);
		`)
		var imgs []*ai.ImageContent
		var savedPaths []string
		for _, item := range tr.Content {
			switch x := item.(type) {
			case *ai.ImageContent:
				imgs = append(imgs, x)
			case *ai.TextContent:
				if p := tascExtractSavedPath(x.Text); p != "" {
					savedPaths = append(savedPaths, p)
				}
			}
		}
		if len(imgs) != 2 || imgs[0].Data != tascPngBase64B || imgs[1].Data != tascPngBase64A {
			t.Fatalf("unexpected image order: len=%d imgs=%+v", len(imgs), imgs)
		}
		if len(savedPaths) != 2 {
			t.Fatalf("expected 2 saved image paths before images, got %v (content: %s)", savedPaths, tascResultText(tr))
		}
		for i, p := range savedPaths {
			raw, err := os.ReadFile(p)
			if err != nil {
				t.Fatalf("ReadFile(%q): %v", p, err)
			}
			wantB64 := tascPngBase64B
			if i == 1 {
				wantB64 = tascPngBase64A
			}
			wantBytes, _ := base64.StdEncoding.DecodeString(wantB64)
			if string(raw) != string(wantBytes) {
				t.Fatalf("saved image %d bytes mismatch", i)
			}
		}
		// Verify saved path text appears before its corresponding image block and "between" separates them.
		fullText := tascResultText(tr)
		if !strings.Contains(fullText, "between") {
			t.Fatalf("expected 'between' marker in content: %s", fullText)
		}
	})

	t.Run("marks where each text item starts, including multi-line text", func(t *testing.T) {
		h := tascCreateHarness(t, nil)
		h.Session.SetActiveToolsByName([]string{"codemode"})
		tr := tascRunCodemode(t, h, `
			console.log("line1\nline2");
			text("alpha\nbeta");
			return "omega";
		`)
		out := tascResultText(tr)
		if !strings.Contains(out, "==> text 1/3 <==") || !strings.Contains(out, "==> text 2/3 <==") || !strings.Contains(out, "==> text 3/3 <==") {
			t.Fatalf("expected text item headers 1/3..3/3, got:\n%s", out)
		}
		if !strings.Contains(out, "line1\nline2") || !strings.Contains(out, "alpha\nbeta") || !strings.Contains(out, "omega") {
			t.Fatalf("expected all text segments in output, got:\n%s", out)
		}
	})

	t.Run("reports script failures as results that keep partial output and the calls that ran", func(t *testing.T) {
		ext := core.InlineExtension{
			Name: "ok-then-fail",
			Factory: func(pi core.ExtensionAPI) error {
				pi.RegisterTool(core.ToolDefinition{
					Name:        "record_step",
					Label:       "Record",
					Description: "Records call",
					Exposure:    core.ToolExposureCodemode,
					Parameters:  tascObjectSchema(),
					Execute: func(context.Context, string, *jsonx.Object, agent.AgentToolUpdateCallback, core.ExtensionToolContext) (agent.AgentToolResult, error) {
						return agent.AgentToolResult{Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "recorded"}}}, nil
					},
				})
				return nil
			},
		}
		h := tascCreateHarness(t, &tascHarnessOptions{Extensions: []core.InlineExtension{ext}})
		h.Session.SetActiveToolsByName([]string{"codemode", "record_step"})
		tr := tascRunCodemode(t, h, `
			text("before boom");
			await tools.record_step({});
			throw new Error("boom");
		`)
		if !tr.IsError {
			t.Fatalf("expected IsError=true on thrown script error")
		}
		out := tascResultText(tr)
		if !strings.Contains(out, "Script failed") || !strings.Contains(out, "before boom") || !strings.Contains(out, "boom") || !strings.Contains(out, "record_step (ok)") {
			t.Fatalf("expected partial output + boom error + call summary, got:\n%s", out)
		}
		rawDet, _ := tr.Details.Get()
		det, ok := rawDet.(codemodeext.CodemodeToolDetails)
		if !ok {
			t.Fatalf("Details = %T, want CodemodeToolDetails", rawDet)
		}
		if len(det.Calls) != 1 || det.Calls[0].Name != "record_step" || det.Calls[0].Status != codemodeext.CodemodeNestedCallOk {
			t.Fatalf("unexpected details calls: %+v", det.Calls)
		}
	})

	t.Run("applies the timeout_ms option and rejects invalid options", func(t *testing.T) {
		h := tascCreateHarness(t, nil)
		h.Session.SetActiveToolsByName([]string{"codemode"})

		trBad := tascRunCodemode(t, h, "// @options: {\"timeout_ms\": 0}\nreturn 1;")
		if !trBad.IsError {
			t.Fatalf("expected invalid options error")
		}

		trTimeout := tascRunCodemode(t, h, "// @options: {\"timeout_ms\": 60}\nwhile (true) {}")
		if !trTimeout.IsError || !strings.Contains(tascResultText(trTimeout), "Script timed out:") {
			t.Fatalf("expected Script timed out error, got: %s", tascResultText(trTimeout))
		}
	})

	t.Run("limits script memory so runaway allocations fail inside the script", func(t *testing.T) {
		h := tascCreateHarness(t, nil)
		h.Session.SetActiveToolsByName([]string{"codemode"})
		tr := tascRunCodemode(t, h, `
			const chunks = [];
			for (let i = 0; i < 4096; i++) {
				chunks.push("x".repeat(1024 * 1024));
			}
			return chunks.length;
		`)
		if !tr.IsError || !strings.Contains(tascResultText(tr), "Script failed") {
			t.Fatalf("expected memory exhaustion script failure, got isError=%v out=%s", tr.IsError, tascResultText(tr))
		}
	})

	t.Run("truncates output to the token budget and spills the full text", func(t *testing.T) {
		h := tascCreateHarness(t, nil)
		h.Session.SetActiveToolsByName([]string{"codemode"})
		tr := tascRunCodemode(t, h, "// @options: {\"max_output_tokens\": 50}\nreturn \"line-\".repeat(2000);")
		if tr.IsError {
			t.Fatalf("unexpected error: %s", tascResultText(tr))
		}
		out := tascResultText(tr)
		if !strings.Contains(out, "Warning: truncated output") || !strings.Contains(out, "[Full output:") {
			t.Fatalf("expected truncated warning + spilled file reference, got:\n%s", out)
		}
		rawDet, _ := tr.Details.Get()
		det, ok := rawDet.(codemodeext.CodemodeToolDetails)
		if !ok || det.FullOutputPath == nil {
			t.Fatalf("expected FullOutputPath in Details, got %+v", rawDet)
		}
		spilled, err := os.ReadFile(*det.FullOutputPath)
		if err != nil {
			t.Fatalf("ReadFile(%q): %v", *det.FullOutputPath, err)
		}
		if len(spilled) < 5000 {
			t.Fatalf("expected full spilled output >= 5000 bytes, got %d", len(spilled))
		}
	})

	t.Run("formats output at the item limit in linear time", func(t *testing.T) {
		h := tascCreateHarness(t, nil)
		h.Session.SetActiveToolsByName([]string{"codemode"})
		start := time.Now()
		tr := tascRunCodemode(t, h, `
			for (let i = 0; i < 600; i++) {
				text("item-" + i);
			}
			return "done";
		`)
		elapsed := time.Since(start)
		if tr.IsError {
			t.Fatalf("unexpected error: %s", tascResultText(tr))
		}
		if elapsed > 5*time.Second {
			t.Fatalf("600 items took too long (%v)", elapsed)
		}
		if !strings.Contains(tascResultText(tr), "item-599") {
			t.Fatalf("expected items formatted into output")
		}
	})

	t.Run("resolves bash calls to structured results, also for non-zero exit codes", func(t *testing.T) {
		h := tascCreateHarness(t, nil)
		h.Session.SetActiveToolsByName([]string{"bash", "codemode"})
		tr := tascRunCodemode(t, h, `
			const ok = await tools.bash({ command: "printf 'hello-bash'" });
			const fail = await tools.bash({ command: "printf 'oops-bash'; exit 7" });
			return JSON.stringify({
				okCode: ok.exit_code,
				okOut: ok.output,
				failCode: fail.exit_code,
				failOut: fail.output,
			});
		`)
		if tr.IsError {
			t.Fatalf("unexpected error: %s", tascResultText(tr))
		}
		out := tascResultText(tr)
		if !strings.Contains(out, `"okCode":0`) || !strings.Contains(out, "hello-bash") || !strings.Contains(out, `"failCode":7`) || !strings.Contains(out, "oops-bash") {
			t.Fatalf("unexpected bash structured results: %s", out)
		}
	})

	t.Run("resolves read calls to text for text files and to image blocks that image() shows", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("alpha\nbeta\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		pngBytes, _ := base64.StdEncoding.DecodeString(tascPngBase64A)
		if err := os.WriteFile(filepath.Join(dir, "pixel.png"), pngBytes, 0o600); err != nil {
			t.Fatal(err)
		}
		h := tascCreateHarness(t, &tascHarnessOptions{Cwd: dir})
		h.Session.SetActiveToolsByName([]string{"read", "codemode"})
		tr := tascRunCodemode(t, h, `
			const txt = await tools.read({ path: "hello.txt" });
			const img = await tools.read({ path: "pixel.png" });
			text("file=" + JSON.stringify(txt));
			image(img);
		`)
		if tr.IsError {
			t.Fatalf("unexpected error: %s", tascResultText(tr))
		}
		if !strings.Contains(tascResultText(tr), "alpha") {
			t.Fatalf("expected text file content, got %s", tascResultText(tr))
		}
		var imgCount int
		for _, item := range tr.Content {
			if ic, ok := item.(*ai.ImageContent); ok && ic != nil && ic.MimeType == "image/png" {
				imgCount++
			}
		}
		if imgCount != 1 {
			t.Fatalf("expected 1 image content block attached via image(read(...)), got %d", imgCount)
		}
	})

	t.Run("persists store() writes as custom entries for later calls", func(t *testing.T) {
		h := tascCreateHarness(t, nil)
		h.Session.SetActiveToolsByName([]string{"codemode"})

		tr1 := tascRunCodemode(t, h, `store("count", 41); store("tag", "alpha"); return load("count");`)
		if tr1.IsError || !strings.Contains(tascResultText(tr1), "41") {
			t.Fatalf("turn 1 failed: %s", tascResultText(tr1))
		}

		tr2 := tascRunCodemode(t, h, `
			const c = load("count");
			store("tag", undefined);
			const deleted = load("tag");
			return JSON.stringify({ c, deletedNull: deleted === undefined });
		`)
		if tr2.IsError || !strings.Contains(tascResultText(tr2), `"c":41`) || !strings.Contains(tascResultText(tr2), `"deletedNull":true`) {
			t.Fatalf("turn 2 store state mismatch: %s", tascResultText(tr2))
		}

		var storeEntries int
		for _, e := range h.SessionManager.GetEntries() {
			if ce, ok := e.(*core.CustomEntry); ok && ce.CustomType == codemodeext.CodemodeStoreEntryType {
				storeEntries++
			}
		}
		if storeEntries != 2 {
			t.Fatalf("expected 2 %q custom entries, got %d", codemodeext.CodemodeStoreEntryType, storeEntries)
		}
	})

	t.Run("appends nothing for failed scripts or scripts without writes", func(t *testing.T) {
		h := tascCreateHarness(t, nil)
		h.Session.SetActiveToolsByName([]string{"codemode"})

		countStoreEntries := func() int {
			n := 0
			for _, e := range h.SessionManager.GetEntries() {
				if ce, ok := e.(*core.CustomEntry); ok && ce.CustomType == codemodeext.CodemodeStoreEntryType {
					n++
				}
			}
			return n
		}

		trNoWrite := tascRunCodemode(t, h, `return load("missing") === undefined;`)
		if trNoWrite.IsError || countStoreEntries() != 0 {
			t.Fatalf("no-write script appended store entries or failed: count=%d out=%s", countStoreEntries(), tascResultText(trNoWrite))
		}

		trFail := tascRunCodemode(t, h, `store("k", 1); throw new Error("abort write");`)
		if !trFail.IsError || countStoreEntries() != 0 {
			t.Fatalf("failed script should not persist store writes: count=%d isErr=%v", countStoreEntries(), trFail.IsError)
		}
	})

	t.Run("runs without a session, starting from an empty store", func(t *testing.T) {
		def := codemodeext.CreateCodemodeToolDefinition(nil)
		res, err := def.Execute(context.Background(), "standalone-call", tascMakeObj("code", `
			const before = load("x") === undefined;
			store("x", "hello");
			const after = load("x");
			return JSON.stringify({ before, after });
		`), nil, nil)
		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}
		if res.IsError != nil && *res.IsError {
			t.Fatalf("unexpected script error: %+v", res)
		}
		var joined strings.Builder
		for _, c := range res.Content {
			if tc, ok := c.(*ai.TextContent); ok && tc != nil {
				joined.WriteString(tc.Text)
			}
		}
		if !strings.Contains(joined.String(), `"before":true`) || !strings.Contains(joined.String(), `"after":"hello"`) {
			t.Fatalf("unexpected standalone store output: %s", joined.String())
		}
	})

	t.Run("loads the values written on the current branch", func(t *testing.T) {
		h := tascCreateHarness(t, nil)
		h.Session.SetActiveToolsByName([]string{"codemode"})

		// Write base key on trunk.
		tascRunCodemode(t, h, `store("shared", "root");`)
		branchPointLeaf := h.SessionManager.GetLeafId()
		if branchPointLeaf == nil {
			t.Fatal("expected non-nil leaf after first turn")
		}

		// Write on Branch A.
		tascRunCodemode(t, h, `store("branchKey", "A");`)
		trA := tascRunCodemode(t, h, `return JSON.stringify({ shared: load("shared"), branchKey: load("branchKey") });`)
		if !strings.Contains(tascResultText(trA), `"branchKey":"A"`) {
			t.Fatalf("branch A value missing: %s", tascResultText(trA))
		}

		// Navigate back to branch point (create Branch B) and verify branchKey is absent while shared remains.
		if _, err := h.Session.NavigateTree(context.Background(), *branchPointLeaf, &core.NavigateTreeOptions{Summarize: false}); err != nil {
			t.Fatalf("NavigateTree failed: %v", err)
		}
		trB := tascRunCodemode(t, h, `return JSON.stringify({ shared: load("shared"), missing: load("branchKey") === undefined });`)
		if !strings.Contains(tascResultText(trB), `"shared":"root"`) || !strings.Contains(tascResultText(trB), `"missing":true`) {
			t.Fatalf("branch B leaked branch A state: %s", tascResultText(trB))
		}
	})

	t.Run("folds store entries from the root, ignoring malformed data", func(t *testing.T) {
		sm := core.SessionManagerInMemory()
		set1 := omap.NewMap[string, any]()
		set1.Set("a", "first")
		set1.Set("b", "to-delete")
		_, _ = sm.AppendCustomEntry(codemodeext.CodemodeStoreEntryType, codemodeext.CodemodeStoreEntryData{
			Set:    set1,
			Delete: []string{},
		})
		// Malformed non-store custom entry.
		_, _ = sm.AppendCustomEntry("unrelated", jsonx.ObjectOf("x", 1))
		// Malformed store entry (wrong data type / nil / scalar).
		_, _ = sm.AppendCustomEntry(codemodeext.CodemodeStoreEntryType, "corrupt-string")
		// Valid overwrite + delete entry.
		set2 := omap.NewMap[string, any]()
		set2.Set("a", "second")
		_, _ = sm.AppendCustomEntry(codemodeext.CodemodeStoreEntryType, codemodeext.CodemodeStoreEntryData{
			Set:    set2,
			Delete: []string{"b"},
		})

		h := tascCreateHarness(t, &tascHarnessOptions{SessionManager: sm})
		h.Session.SetActiveToolsByName([]string{"codemode"})
		tr := tascRunCodemode(t, h, `return JSON.stringify({ a: load("a"), bMissing: load("b") === undefined });`)
		out := tascResultText(tr)
		if !strings.Contains(out, `"a":"second"`) || !strings.Contains(out, `"bMissing":true`) {
			t.Fatalf("folded store mismatch: %s", out)
		}
	})

	t.Run("declares models only for the session's own codemode tool", func(t *testing.T) {
		h := tascCreateHarness(t, &tascHarnessOptions{
			OmitDefaultCodemode: true,
			Extensions: []core.InlineExtension{
				tascCodemodeExtension(&codemodeext.CodemodeExtensionOptions{
					Models: tascBoolPtr(false),
				}),
			},
		})
		h.Session.SetActiveToolsByName([]string{"codemode"})
		var cmDesc string
		stopDone := ai.StopReasonStop
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseFactory(func(_ context.Context, tr *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
				for _, tool := range ai.GetCurrentTools(tr.Messages) {
					if tool.Name == codemodeext.CodemodeToolName {
						cmDesc = tool.Description
					}
				}
				return providers.FauxAssistantMessage(providers.FauxAssistantText("ok"), &providers.FauxAssistantMessageOptions{StopReason: &stopDone}), nil
			}),
		})
		if err := h.Session.Prompt(context.Background(), "check models off"); err != nil {
			t.Fatalf("Prompt failed: %v", err)
		}
		if strings.Contains(cmDesc, "models.classify") || strings.Contains(cmDesc, "models.generateImages") {
			t.Fatalf("expected models namespace omitted from description when Models=false, got:\n%s", cmDesc)
		}
		tr := tascRunCodemode(t, h, `return typeof models;`)
		if !strings.Contains(tascResultText(tr), "undefined") {
			t.Fatalf("expected typeof models === 'undefined' when Models=false, got %s", tascResultText(tr))
		}
	})

	t.Run("lists models and classifies with catalog auth, ignoring script-supplied fields", func(t *testing.T) {
		h := tascCreateHarness(t, nil)
		h.Session.SetActiveToolsByName([]string{"codemode"})

		var sawKey *string
		var sawAuthHeader *string
		if err := h.Session.ModelRegistry().RegisterProvider("mock-clf-prov", core.ProviderConfigInput{
			BaseUrl: tascStrPtr("https://catalog.example.com"),
			ApiKey:  tascStrPtr("catalog-secret-key"),
			Classifiers: map[string]ai.ProviderClassifier{
				"mock-clf": {
					Classify: func(_ context.Context, model *ai.ClassifierModel, _ *ai.ClassifierContext, opts *ai.ClassifierOptions) (*ai.ClassifierResult, error) {
						sawKey = opts.ApiKey
						if opts.Headers != nil {
							if v, ok := opts.Headers.Get("authorization"); ok {
								sawAuthHeader = v
							}
						}
						ans := omap.NewMap[string, ai.ClassifierAnswer]()
						ans.Set("q1", &ai.ClassifierBoolAnswer{Type: "bool", Probability: 0.9})
						return &ai.ClassifierResult{
							Api:        ai.ClassifierApi(model.Api),
							Provider:   model.Provider,
							Model:      model.Id,
							Answers:    ans,
							StopReason: ai.ClassifierStopReasonStop,
							Timestamp:  1,
						}, nil
					},
				},
			},
			Models: []core.ProviderModelConfig{
				&core.ProviderClassifierModelConfig{
					ProviderModelConfigBase: core.ProviderModelConfigBase{Id: "clf-test", Name: "Clf Test", Api: tascStrPtr("mock-clf")},
					Type:                    ai.ModelTypeClassifier,
					ContextWindow:           16000,
				},
			},
		}); err != nil {
			t.Fatalf("RegisterProvider failed: %v", err)
		}

		tr := tascRunCodemode(t, h, `
			const listed = await models.getAvailableOfType("classifier", "mock-clf-prov");
			const clfModel = await models.getModelOfType("classifier", "mock-clf-prov", "clf-test");
			const res = await models.classify(
				clfModel,
				{
					state: { topic: "test" },
					questions: {
						q1: { type: "bool", instructions: "ok?", criteria: { true: "yes", false: "no" } }
					}
				},
				{
					apiKey: "evil-script-key",
					headers: { Authorization: "Bearer evil", "x-safe": "kept" }
				}
			);
			return JSON.stringify({ listedCount: listed.length, prob: res.answers.q1.probability });
		`)
		if tr.IsError {
			t.Fatalf("unexpected error: %s", tascResultText(tr))
		}
		if sawKey == nil || *sawKey != "catalog-secret-key" {
			t.Fatalf("expected catalog apiKey 'catalog-secret-key', got %v", sawKey)
		}
		if sawAuthHeader != nil {
			t.Fatalf("expected script-supplied Authorization header stripped, got %q", *sawAuthHeader)
		}
		if out := tascResultText(tr); !strings.Contains(out, `"listedCount":1`) || !strings.Contains(out, `"prob":0.9`) {
			t.Fatalf("unexpected classify output: %s", out)
		}
	})

	t.Run("generates images with catalog auth and attaches them through image()", func(t *testing.T) {
		h := tascCreateHarness(t, nil)
		h.Session.SetActiveToolsByName([]string{"codemode"})

		var sawKey *string
		if err := h.Session.ModelRegistry().RegisterProvider("mock-img-prov", core.ProviderConfigInput{
			BaseUrl: tascStrPtr("https://img.example.com"),
			ApiKey:  tascStrPtr("img-catalog-key"),
			Images: map[string]ai.ProviderImages{
				"mock-img": {
					GenerateImages: func(_ context.Context, model *ai.ImageModel, _ *ai.ImagesContext, opts *ai.ImagesOptions) (*ai.AssistantImages, error) {
						sawKey = opts.ApiKey
						return &ai.AssistantImages{
							Api:        ai.ImageApi(model.Api),
							Provider:   model.Provider,
							Model:      model.Id,
							Output:     ai.ContentList{&ai.ImageContent{Type: "image", Data: tascPngBase64A, MimeType: "image/png"}},
							StopReason: ai.ImagesStopReasonStop,
							Usage:      &ai.Usage{Cost: ai.UsageCost{Total: 0.04}},
							Timestamp:  1,
						}, nil
					},
				},
			},
			Models: []core.ProviderModelConfig{
				&core.ProviderImageModelConfig{
					ProviderModelConfigBase: core.ProviderModelConfigBase{Id: "img-test", Name: "Img Test", Api: tascStrPtr("mock-img")},
					Type:                    ai.ModelTypeImage,
					Output:                  []ai.ModelModality{ai.ModelModalityImage},
				},
			},
		}); err != nil {
			t.Fatalf("RegisterProvider failed: %v", err)
		}

		tr := tascRunCodemode(t, h, `
			const imgModel = await models.getModelOfType("image", "mock-img-prov", "img-test");
			const res = await models.generateImages(
				imgModel,
				{ input: [{ type: "text", text: "cat" }] },
				{ apiKey: "spoofed" }
			);
			image(res.output[0]);
			return res.output.length;
		`)
		if tr.IsError {
			t.Fatalf("unexpected error: %s", tascResultText(tr))
		}
		if sawKey == nil || *sawKey != "img-catalog-key" {
			t.Fatalf("expected catalog apiKey 'img-catalog-key', got %v", sawKey)
		}
		var foundImg bool
		for _, item := range tr.Content {
			if ic, ok := item.(*ai.ImageContent); ok && ic != nil && ic.Data == tascPngBase64A {
				foundImg = true
			}
		}
		if !foundImg {
			t.Fatalf("expected generated image attached in content")
		}
		if tr.Usage == nil || tr.Usage.Cost.Total < 0.039 || tr.Usage.Cost.Total > 0.041 {
			t.Fatalf("expected image generation cost recorded on Usage, got %+v", tr.Usage)
		}
	})

	t.Run("notes generated images that the script did not show", func(t *testing.T) {
		h := tascCreateHarness(t, nil)
		h.Session.SetActiveToolsByName([]string{"codemode"})
		if err := h.Session.ModelRegistry().RegisterProvider("mock-img-unshown", core.ProviderConfigInput{
			BaseUrl: tascStrPtr("https://img.example.com"),
			ApiKey:  tascStrPtr("k"),
			Images: map[string]ai.ProviderImages{
				"mock-img": {
					GenerateImages: func(_ context.Context, model *ai.ImageModel, _ *ai.ImagesContext, _ *ai.ImagesOptions) (*ai.AssistantImages, error) {
						return &ai.AssistantImages{
							Api:      ai.ImageApi(model.Api),
							Provider: model.Provider,
							Model:    model.Id,
							Output: ai.ContentList{
								&ai.ImageContent{Type: "image", Data: tascPngBase64A, MimeType: "image/png"},
								&ai.ImageContent{Type: "image", Data: tascPngBase64B, MimeType: "image/png"},
							},
							StopReason: ai.ImagesStopReasonStop,
							Timestamp:  1,
						}, nil
					},
				},
			},
			Models: []core.ProviderModelConfig{
				&core.ProviderImageModelConfig{
					ProviderModelConfigBase: core.ProviderModelConfigBase{Id: "img-2", Name: "Img 2", Api: tascStrPtr("mock-img")},
					Type:                    ai.ModelTypeImage,
					Output:                  []ai.ModelModality{ai.ModelModalityImage},
				},
			},
		}); err != nil {
			t.Fatalf("RegisterProvider failed: %v", err)
		}

		tr := tascRunCodemode(t, h, `
			const imgModel = await models.getModelOfType("image", "mock-img-unshown", "img-2");
			await models.generateImages(
				imgModel,
				{ input: [{ type: "text", text: "two cats" }] }
			);
			return "forgot to call image()";
		`)
		if tr.IsError {
			t.Fatalf("unexpected error: %s", tascResultText(tr))
		}
		if out := tascResultText(tr); !strings.Contains(out, "models.generateImages() returned 2 images that the script did not show") {
			t.Fatalf("expected unshown generated images note, got:\n%s", out)
		}
	})

	t.Run("reports provider errors as results and invalid arguments as exceptions", func(t *testing.T) {
		h := tascCreateHarness(t, nil)
		h.Session.SetActiveToolsByName([]string{"codemode"})
		if err := h.Session.ModelRegistry().RegisterProvider("mock-err-prov", core.ProviderConfigInput{
			BaseUrl: tascStrPtr("https://err.example.com"),
			ApiKey:  tascStrPtr("k"),
			Classifiers: map[string]ai.ProviderClassifier{
				"mock-clf-err": {
					Classify: func(_ context.Context, _ *ai.ClassifierModel, _ *ai.ClassifierContext, _ *ai.ClassifierOptions) (*ai.ClassifierResult, error) {
						return nil, errors.New("upstream classifier unavailable")
					},
				},
			},
			Models: []core.ProviderModelConfig{
				&core.ProviderClassifierModelConfig{
					ProviderModelConfigBase: core.ProviderModelConfigBase{Id: "clf-err", Name: "Clf Err", Api: tascStrPtr("mock-clf-err")},
					Type:                    ai.ModelTypeClassifier,
					ContextWindow:           8000,
				},
			},
		}); err != nil {
			t.Fatalf("RegisterProvider failed: %v", err)
		}

		tr := tascRunCodemode(t, h, `
			let threw = false;
			try {
				await models.classify({ provider: "nonexistent", id: "nope" }, { state: {}, questions: {} });
			} catch (e) {
				threw = true;
			}
			const clfModel = await models.getModelOfType("classifier", "mock-err-prov", "clf-err");
			const provRes = await models.classify(
				clfModel,
				{
					state: { ok: true },
					questions: {
						q1: { type: "bool", instructions: "ok?", criteria: { true: "yes", false: "no" } }
					}
				}
			);
			return JSON.stringify({ threw, stopReason: provRes.stopReason, errMsg: provRes.errorMessage });
		`)
		if tr.IsError {
			t.Fatalf("unexpected error: %s", tascResultText(tr))
		}
		out := tascResultText(tr)
		if !strings.Contains(out, `"threw":true`) || !strings.Contains(out, `"stopReason":"error"`) || !strings.Contains(out, "upstream classifier unavailable") {
			t.Fatalf("unexpected error handling output: %s", out)
		}
	})

	t.Run("integrates toolsearchext alongside codemode", func(t *testing.T) {
		h := tascCreateHarness(t, &tascHarnessOptions{
			Extensions: []core.InlineExtension{
				{Name: "tool-search", Factory: toolsearchext.Extension},
			},
		})
		h.Session.SetActiveToolsByName([]string{"codemode", "tool_search", "read", "bash"})
		if !strings.Contains(strings.Join(h.Session.GetActiveToolNames(), ","), "tool_search") {
			t.Fatalf("expected tool_search active: %v", h.Session.GetActiveToolNames())
		}
	})

	t.Run("emits null when script returns null and omits undefined return values", func(t *testing.T) {
		h := tascCreateHarness(t, nil)
		h.Session.SetActiveToolsByName([]string{"codemode"})

		nullRes := tascRunCodemode(t, h, `return null;`)
		if nullRes.IsError {
			t.Fatalf("unexpected error for return null: %s", tascResultText(nullRes))
		}
		if len(nullRes.Content) != 2 {
			t.Fatalf("expected header + output item for return null, got %d items: %q", len(nullRes.Content), tascResultText(nullRes))
		}
		if got := nullRes.Content[1].(*ai.TextContent).Text; got != "null" {
			t.Fatalf("expected return null output item %q, got %q", "null", got)
		}

		consoleAndNullRes := tascRunCodemode(t, h, `console.log("before"); return null;`)
		if consoleAndNullRes.IsError {
			t.Fatalf("unexpected error for console + return null: %s", tascResultText(consoleAndNullRes))
		}
		if len(consoleAndNullRes.Content) != 2 {
			t.Fatalf("expected 2 content items, got %d: %q", len(consoleAndNullRes.Content), tascResultText(consoleAndNullRes))
		}
		if got := consoleAndNullRes.Content[1].(*ai.TextContent).Text; got != "==> text 1/2 <==\nbefore\n==> text 2/2 <==\nnull" {
			t.Fatalf("expected %q, got %q", "==> text 1/2 <==\nbefore\n==> text 2/2 <==\nnull", got)
		}

		undefRes := tascRunCodemode(t, h, `console.log("only console"); return undefined;`)
		if undefRes.IsError {
			t.Fatalf("unexpected error for return undefined: %s", tascResultText(undefRes))
		}
		if len(undefRes.Content) != 2 {
			t.Fatalf("expected 2 content items, got %d: %q", len(undefRes.Content), tascResultText(undefRes))
		}
		if got := undefRes.Content[1].(*ai.TextContent).Text; got != "only console" {
			t.Fatalf("expected undefined return to be omitted and yield %q, got %q", "only console", got)
		}

		bareUndefRes := tascRunCodemode(t, h, `return undefined;`)
		if bareUndefRes.IsError {
			t.Fatalf("unexpected error for bare return undefined: %s", tascResultText(bareUndefRes))
		}
		if len(bareUndefRes.Content) != 1 {
			t.Fatalf("expected only header item when script returns undefined with no output, got %d items: %q", len(bareUndefRes.Content), tascResultText(bareUndefRes))
		}
	})
}
