package core

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/tui"
)

func TestToolsIndexAndStrictMode(t *testing.T) {
	dir := t.TempDir()

	t.Run("AllToolNames and factory sets cover all 8 built-in tools", func(t *testing.T) {
		wantNames := []ToolName{
			ToolNameRead,
			ToolNameBash,
			ToolNamePowershell,
			ToolNameEdit,
			ToolNameWrite,
			ToolNameGrep,
			ToolNameFind,
			ToolNameLs,
		}
		if !slices.Equal(AllToolNames, wantNames) {
			t.Fatalf("AllToolNames = %v, want %v", AllToolNames, wantNames)
		}

		allTools := CreateAllTools(dir)
		if len(allTools) != len(AllToolNames) {
			t.Fatalf("CreateAllTools returned %d tools, want %d", len(allTools), len(AllToolNames))
		}
		for _, name := range wantNames {
			if _, ok := allTools[name]; !ok {
				t.Fatalf("missing tool %q in CreateAllTools", name)
			}
			if !AllToolNamesSet[name] {
				t.Fatalf("AllToolNamesSet[%q] = false", name)
			}
		}

		coding := CreateCodingTools(dir)
		if len(coding) != 4 {
			t.Fatalf("CreateCodingTools len = %d, want 4", len(coding))
		}
		readOnly := CreateReadOnlyTools(dir)
		if len(readOnly) != 4 {
			t.Fatalf("CreateReadOnlyTools len = %d, want 4", len(readOnly))
		}
	})

	t.Run("builtin strict ConstrainedSampling applies to strictToolNames only", func(t *testing.T) {
		defs := CreateAllToolDefinitions(dir)
		strictSet := map[ToolName]bool{
			ToolNameRead:       true,
			ToolNameBash:       true,
			ToolNamePowershell: true,
			ToolNameEdit:       true,
			ToolNameWrite:      true,
		}
		for _, name := range AllToolNames {
			def, ok := defs[name]
			if !ok {
				t.Fatalf("missing definition %q", name)
			}
			isStrict := def.ConstrainedSampling.V != nil
			if !strictSet[name] {
				if isStrict {
					t.Fatalf("%q tool should leave ConstrainedSampling unset", name)
				}
			} else {
				cs, ok := def.ConstrainedSampling.V.(*ai.ConstrainedSamplingJsonSchema)
				if !ok || cs.Strict != ai.ConstrainedSamplingStrictPrefer {
					t.Fatalf("expected prefer json_schema strict mode on %q, got %#v", name, def.ConstrainedSampling.V)
				}
			}
		}
	})

	t.Run("WrapToolDefinition preserves prepareArguments and context forwarding", func(t *testing.T) {
		var sawCtxCwd string
		var prepared bool
		def := ToolDefinition{
			Name:                "custom",
			Label:               "Custom",
			Description:         "custom desc",
			ConstrainedSampling: tiStrictPreferJsonSchema(),
			PrepareArguments: func(args any) any {
				prepared = true
				return args
			},
			Execute: func(_ context.Context, _ string, params *jsonx.Object, _ agent.AgentToolUpdateCallback, ectx ExtensionToolContext) (agent.AgentToolResult, error) {
				if ectx != nil {
					sawCtxCwd = ectx.Cwd()
				}
				v, _ := params.GetString("x")
				return agent.AgentToolResult{
					Content: ai.ContentList{&ai.TextContent{Type: "text", Text: v}},
				}, nil
			},
		}

		wrapped := WrapToolDefinition(def, func(_ context.Context, _ string) ExtensionToolContext {
			return &ttrMockToolContext{cwd: dir}
		})
		wrapped.PrepareArguments(jsonx.ObjectOf("x", "ok"))
		if !prepared {
			t.Fatalf("PrepareArguments was not forwarded")
		}
		res, err := wrapped.Execute(context.Background(), "c1", jsonx.ObjectOf("x", "ok"), nil)
		if err != nil {
			t.Fatal(err)
		}
		if sawCtxCwd != dir {
			t.Fatalf("expected factory cwd %q, got %q", dir, sawCtxCwd)
		}
		if ttrTestTextOutput(res) != "ok" {
			t.Fatalf("unexpected text output %q", ttrTestTextOutput(res))
		}

		overrideCtx := WithExtensionToolContext(context.Background(), &ttrMockToolContext{cwd: "/override"})
		if _, err := wrapped.Execute(overrideCtx, "c2", jsonx.ObjectOf("x", "ok"), nil); err != nil {
			t.Fatal(err)
		}
		if sawCtxCwd != "/override" {
			t.Fatalf("expected explicit context /override to take precedence, got %q", sawCtxCwd)
		}
	})

	t.Run("PromptSnippet and PromptGuidelines match BuiltinToolSystemPromptContributions", func(t *testing.T) {
		defs := CreateAllToolDefinitions(dir)
		for _, name := range AllToolNames {
			contrib := BuiltinToolSystemPromptContributions[name]
			def := defs[name]
			if def.PromptSnippet == nil || *def.PromptSnippet != contrib.Snippet {
				t.Fatalf("tool %q PromptSnippet mismatch: %v vs %q", name, def.PromptSnippet, contrib.Snippet)
			}
			if !slices.Equal(def.PromptGuidelines, contrib.Guidelines) {
				t.Fatalf("tool %q PromptGuidelines mismatch: %v vs %v", name, def.PromptGuidelines, contrib.Guidelines)
			}
		}
	})
}

func TestToolRenderersAndUtils(t *testing.T) {
	dir := t.TempDir()

	t.Run("FormatReadLineRange formats offset and limit consistently", func(t *testing.T) {
		if got := FormatReadLineRange(nil, nil); got != "" {
			t.Fatalf("got %q", got)
		}
		if got := FormatReadLineRange(float64(10), float64(5)); got != ":10-14" {
			t.Fatalf("got %q, want :10-14", got)
		}
		if got := FormatReadLineRange(float64(3), nil); got != ":3" {
			t.Fatalf("got %q, want :3", got)
		}
	})

	t.Run("CreateAllToolRenderers and WithBuiltInRenderers render call and result", func(t *testing.T) {
		all := CreateAllToolRenderers()
		if len(all) != len(AllToolNames) {
			t.Fatalf("expected %d tool renderers, got %d", len(AllToolNames), len(all))
		}

		merged := WithBuiltInRenderers("read", &ToolRenderers{})
		if merged == nil || merged.RenderCall == nil || merged.RenderResult == nil {
			t.Fatalf("expected built-in read renderers to be filled in")
		}

		skillDir := filepath.Join(dir, "my-skill")
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			t.Fatal(err)
		}
		skillFile := filepath.Join(skillDir, "SKILL.md")
		rctx := ToolRenderContext{
			KeybindingHints: DefaultKeybindingHints(),
			Args:            jsonx.ObjectOf("path", skillFile, "offset", 1, "limit", 10),
			Cwd:             dir,
		}
		callComp := all[ToolNameRead].RenderCall(rctx.Args, nil, rctx)
		if callComp == nil {
			t.Fatalf("expected non-nil call component")
		}
		renderedCall := strings.Join(callComp.Render(100), "\n")
		if !strings.Contains(renderedCall, "[skill]") || !strings.Contains(renderedCall, "my-skill") || !strings.Contains(renderedCall, ":1-10") {
			t.Fatalf("unexpected rendered read call: %q", renderedCall)
		}

		compactRead := all[ToolNameRead].RenderResult(agent.AgentToolResult{
			Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "skill content"}},
		}, ToolRenderResultOptions{Expanded: false}, nil, rctx)
		if len(compactRead.Render(100)) != 0 {
			t.Fatalf("expected collapsed read result without warning to render empty, got %q", strings.Join(compactRead.Render(100), "\n"))
		}
		expandedRead := all[ToolNameRead].RenderResult(agent.AgentToolResult{
			Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "skill content"}},
		}, ToolRenderResultOptions{Expanded: true}, nil, rctx)
		renderedExpanded := strings.Join(expandedRead.Render(100), "\n")
		if !strings.Contains(renderedExpanded, "skill content") {
			t.Fatalf("expected expanded read content, got %q", renderedExpanded)
		}

		writeArgs := jsonx.ObjectOf("path", "hello.go", "content", "package main\nfunc main() {}\n")
		wCall := all[ToolNameWrite].RenderCall(writeArgs, nil, ToolRenderContext{
			KeybindingHints: DefaultKeybindingHints(),
			Args:            writeArgs,
			Cwd:             dir,
		})
		if out := strings.Join(wCall.Render(100), "\n"); !strings.Contains(out, "hello.go") {
			t.Fatalf("unexpected write call render: %q", out)
		}

		sampleFile := filepath.Join(dir, "edit-target.txt")
		if err := os.WriteFile(sampleFile, []byte("before\nmiddle\nafter\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		editArgs := jsonx.ObjectOf("path", "edit-target.txt", "oldText", "middle", "newText", "changed")
		editState := &EditRenderState{}
		invalidated := make(chan struct{}, 2)
		editRctx := ToolRenderContext{
			KeybindingHints: DefaultKeybindingHints(),
			Args:            editArgs,
			Cwd:             dir,
			ArgsComplete:    true,
			State:           editState,
			Invalidate: func() {
				select {
				case invalidated <- struct{}{}:
				default:
				}
			},
		}
		eCall := all[ToolNameEdit].RenderCall(editArgs, nil, editRctx)
		select {
		case <-invalidated:
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for async edit preview diff computation")
		}
		renderedEdit := strings.Join(eCall.Render(100), "\n")
		if !strings.Contains(renderedEdit, "changed") {
			t.Fatalf("expected async edit preview to show replacement text, got %q", renderedEdit)
		}

		for _, name := range []ToolName{ToolNameBash, ToolNamePowershell, ToolNameLs, ToolNameFind, ToolNameGrep} {
			r := all[name]
			args := jsonx.ObjectOf("command", "echo hi", "path", ".", "pattern", "foo")
			comp := r.RenderCall(args, nil, ToolRenderContext{
				KeybindingHints: DefaultKeybindingHints(),
				Args:            args,
				Cwd:             dir,
			})
			if comp == nil || len(comp.Render(80)) == 0 {
				t.Fatalf("empty call render for %q", name)
			}
			resComp := r.RenderResult(agent.AgentToolResult{
				Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "line1\nline2"}},
			}, ToolRenderResultOptions{}, nil, ToolRenderContext{
				KeybindingHints: DefaultKeybindingHints(),
				Args:            args,
				Cwd:             dir,
			})
			if resComp == nil {
				t.Fatalf("nil result render for %q", name)
			}
		}
	})

	t.Run("ToolTimerState cleans up timer on Stop", func(t *testing.T) {
		state := &ToolTimerState{}
		rctx := ToolRenderContext{
			ExecutionStarted: true,
			State:            state,
			Invalidate:       func() {},
		}
		StartToolTimer(rctx)
		if txt := ToolTimerText(rctx, true); txt == nil {
			t.Fatalf("expected non-nil timer text while streaming")
		}
		state.Stop()
		dur := int64(1500)
		rctxDone := ToolRenderContext{State: state, DurationMs: &dur}
		if txt := ToolTimerText(rctxDone, false); txt == nil || *txt != "Took 1.5s" {
			t.Fatalf("unexpected final timer text: %v", txt)
		}
	})
}

func TestBareboneExtensionsShims(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	t.Run("LoadExtensions and DiscoverAndLoadExtensions return 0 extensions and warn on script files", func(t *testing.T) {
		scriptPath := filepath.Join(dir, "ext.ts")
		if err := os.WriteFile(scriptPath, []byte("export default function() {}"), 0o600); err != nil {
			t.Fatal(err)
		}
		loaded := LoadExtensions(ctx, []string{scriptPath}, dir)
		if len(loaded.Extensions) != 0 || len(loaded.Errors) != 0 {
			t.Fatalf("unexpected barebone extensions/errors: %+v", loaded)
		}
		if len(loaded.Warnings) != 1 || !strings.Contains(loaded.Warnings[0].Warning, scriptPath) {
			t.Fatalf("expected JS/TS skip warning, got %+v", loaded.Warnings)
		}

		discovered := DiscoverAndLoadExtensions(ctx, []string{scriptPath}, dir, t.TempDir())
		if len(discovered.Extensions) != 0 || len(discovered.Warnings) != 1 || discovered.Runtime == nil {
			t.Fatalf("unexpected DiscoverAndLoadExtensions result: %+v", discovered)
		}
	})

	t.Run("ExtensionRunner 0-extension methods are safe no-ops", func(t *testing.T) {
		var nilRunner *ExtensionRunner
		if nilRunner.HasHandlers("tool_call") {
			t.Fatalf("expected false from nil runner")
		}
		nilRunner.Shutdown()

		rt := CreateExtensionRuntime()
		runner := NewExtensionRunner(nil, rt, dir, nil, nil)
		if runner.HasHandlers("tool_call") {
			t.Fatalf("expected false for 0 extensions")
		}
		if res := runner.EmitToolCall(ctx, ToolCallEvent{}); res != nil {
			t.Fatalf("expected nil ToolCallEventResult, got %#v", res)
		}
		combined := runner.EmitBeforeAgentStart(ctx, BeforeAgentStartTrigger{Prompt: "hi"}, BuildSystemPromptOptions{})
		if len(combined.Messages) != 0 {
			t.Fatalf("expected 0 injected messages, got %d", len(combined.Messages))
		}
		var gotNativeId string
		runner.BindCore(ExtensionActions{}, ExtensionContextActions{}, &ExtensionProviderActions{
			RegisterNativeProvider: func(provider *ai.Provider) {
				if provider != nil {
					gotNativeId = provider.Id
				}
			},
		})
		rt.RegisterNativeProvider(&ai.Provider{Id: "custom-native"})
		if gotNativeId != "custom-native" {
			t.Fatalf("expected RegisterNativeProvider callback, got %q", gotNativeId)
		}
		runner.Shutdown()
		_ = WrapRegisteredTool(RegisteredTool{
			Definition: ToolDefinition{Name: "t", Description: "d"},
		}, runner)
		var ui tui.Component
		_ = ui
	})
}
