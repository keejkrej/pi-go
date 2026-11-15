package core

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/tui"
)

type terMockUIContext struct {
	erNoOpUIContext
	onSelect func(ctx context.Context, title string, options []string) (*string, error)
}

func (m *terMockUIContext) Select(ctx context.Context, title string, options []string, _ ...*ExtensionUIDialogOptions) (*string, error) {
	if m.onSelect != nil {
		return m.onSelect(ctx, title, options)
	}
	if len(options) > 0 {
		s := options[0]
		return &s, nil
	}
	return nil, nil
}

func (m *terMockUIContext) Confirm(_ context.Context, _, _ string, _ ...*ExtensionUIDialogOptions) (bool, error) {
	return true, nil
}

func (m *terMockUIContext) Input(_ context.Context, title string, _ *string, _ ...*ExtensionUIDialogOptions) (*string, error) {
	return &title, nil
}

func (m *terMockUIContext) Editor(_ context.Context, title string, _ ...string) (*string, error) {
	return &title, nil
}

func (m *terMockUIContext) Custom(
	_ context.Context,
	_ func(tui.TUI, *theme.Theme, *tui.KeybindingsManager, func(any)) (tui.Component, error),
	_ ...*ExtensionCustomOptions,
) (any, error) {
	return "custom-ok", nil
}

type terFakeComp struct{ label string }

func (terFakeComp) Render(int) []string { return nil }
func (terFakeComp) Invalidate()         {}

type terMockBashOps struct{}

func (*terMockBashOps) Exec(context.Context, string, string, BashOperationsOptions) (BashOperationsResult, error) {
	return BashOperationsResult{}, nil
}

func terMakeExt(extPath string) Extension {
	return Extension{
		Path:             extPath,
		ResolvedPath:     extPath,
		SourceInfo:       utils.CreateSyntheticSourceInfo(extPath, utils.SyntheticSourceOptions{}),
		Handlers:         make(map[string][]any),
		Tools:            make(map[string]RegisteredTool),
		MessageRenderers: make(map[string]MessageRenderer),
		EntryRenderers:   make(map[string]EntryRenderer),
		Commands:         make(map[string]RegisteredCommand),
		Flags:            make(map[string]ExtensionFlag),
		Shortcuts:        make(map[tui.KeyId]ExtensionShortcut),
	}
}

func terStrPtr(s string) *string { return &s }
func terBoolPtr(b bool) *bool    { return &b }

func TestExtensionRunnerShortcutsAndCommands(t *testing.T) {
	ext1 := terMakeExt("/ext/one.ts")
	ext1.Shortcuts["ctrl+c"] = ExtensionShortcut{Shortcut: "ctrl+c", ExtensionPath: ext1.Path}
	ext1.Shortcuts["ctrl+t"] = ExtensionShortcut{Shortcut: "ctrl+t", ExtensionPath: ext1.Path, Description: terStrPtr("one")}
	ext1.Shortcuts["ctrl+k"] = ExtensionShortcut{Shortcut: "ctrl+k", ExtensionPath: ext1.Path, Description: terStrPtr("k1")}
	ext1.Commands["dup"] = RegisteredCommand{Name: "dup", Description: terStrPtr("first")}
	ext1.Commands["dup:2"] = RegisteredCommand{Name: "dup:2", Description: terStrPtr("explicit-2")}
	ext1.Tools["alpha"] = RegisteredTool{Definition: ToolDefinition{Name: "alpha", Description: "first"}}
	ext1.Flags["verbose"] = ExtensionFlag{Name: "verbose", Type: "boolean", Default: false, ExtensionPath: ext1.Path}

	ext2 := terMakeExt("/ext/two.ts")
	ext2.Shortcuts["CTRL+K"] = ExtensionShortcut{Shortcut: "CTRL+K", ExtensionPath: ext2.Path, Description: terStrPtr("k2")}
	ext2.Commands["dup"] = RegisteredCommand{Name: "dup", Description: terStrPtr("second")}
	ext2.Commands["solo"] = RegisteredCommand{Name: "solo", Description: terStrPtr("only")}
	ext2.Tools["alpha"] = RegisteredTool{Definition: ToolDefinition{Name: "alpha", Description: "second"}}
	ext2.Tools["beta"] = RegisteredTool{Definition: ToolDefinition{Name: "beta", Description: "beta"}}
	ext2.Flags["verbose"] = ExtensionFlag{Name: "verbose", Type: "string", Default: "override", ExtensionPath: ext2.Path}

	runner := NewExtensionRunner([]Extension{ext1, ext2}, nil, t.TempDir(), nil, nil)
	defer runner.Shutdown()

	if got := runner.GetExtensionPaths(); len(got) != 2 || got[0] != ext1.Path || got[1] != ext2.Path {
		t.Fatalf("unexpected extension paths: %v", got)
	}

	// Tools: first registration wins.
	tools := runner.GetAllRegisteredTools()
	if len(tools) != 2 || tools[0].Definition.Name != "alpha" || tools[0].Definition.Description != "first" {
		t.Fatalf("unexpected registered tools: %+v", tools)
	}
	if def := runner.GetToolDefinition("alpha"); def == nil || def.Description != "first" {
		t.Fatalf("unexpected GetToolDefinition: %+v", def)
	}
	if def := runner.GetToolDefinition("missing"); def != nil {
		t.Fatalf("expected nil for missing tool, got %+v", def)
	}

	// Flags: first registration wins + runtime flag values.
	flags := runner.GetFlags()
	if len(flags) != 1 || flags["verbose"].Type != "boolean" {
		t.Fatalf("unexpected flags: %+v", flags)
	}
	runner.SetFlagValue("verbose", true)
	if vals := runner.GetFlagValues(); vals["verbose"] != true {
		t.Fatalf("unexpected flag values: %+v", vals)
	}

	// Shortcuts: app.clear ("ctrl+c") is restricted; tui.editor.pageUp ("ctrl+t") can be overridden with warning;
	// extension vs extension ("ctrl+k") last wins with warning.
	kb := omap.NewMap[string, *tui.KeybindingKeys]()
	cc := tui.KeyId("ctrl+c")
	ct := tui.KeyId("ctrl+t")
	kb.Set(string(KeybindingAppClear), &tui.KeybindingKeys{Single: &cc})
	kb.Set("tui.editor.pageUp", &tui.KeybindingKeys{Single: &ct})

	shortcuts := runner.GetShortcuts(kb)
	if _, hasClear := shortcuts["ctrl+c"]; hasClear {
		t.Fatalf("expected reserved built-in shortcut ctrl+c to be skipped")
	}
	if s, ok := shortcuts["ctrl+t"]; !ok || s.ExtensionPath != ext1.Path {
		t.Fatalf("expected non-reserved built-in ctrl+t override to succeed, got %+v", shortcuts)
	}
	if s, ok := shortcuts["ctrl+k"]; !ok || s.ExtensionPath != ext2.Path {
		t.Fatalf("expected last extension to win on ctrl+k, got %+v", shortcuts)
	}
	diags := runner.GetShortcutDiagnostics()
	if len(diags) != 3 {
		t.Fatalf("expected 3 shortcut diagnostics, got %d: %+v", len(diags), diags)
	}

	// Commands: duplicate suffixing avoids colliding with explicit "dup:2".
	cmds := runner.GetRegisteredCommands()
	if got := runner.GetCommandDiagnostics(); len(got) != 0 {
		t.Fatalf("expected 0 command diagnostics, got %+v", got)
	}
	names := make([]string, len(cmds))
	for i, c := range cmds {
		names[i] = c.InvocationName
	}
	wantNames := []string{"dup:1", "dup:2", "dup:3", "solo"}
	if len(names) != len(wantNames) {
		t.Fatalf("expected %d commands, got %v", len(wantNames), names)
	}
	for i := range wantNames {
		if names[i] != wantNames[i] {
			t.Fatalf("command[%d] = %q, want %q (all=%v)", i, names[i], wantNames[i], names)
		}
	}
	if cmd := runner.GetCommand("dup:2"); cmd == nil || cmd.Description == nil || *cmd.Description != "explicit-2" {
		t.Fatalf("unexpected GetCommand(dup:2): %+v", cmd)
	}
	if cmd := runner.GetCommand("dup:3"); cmd == nil || cmd.Description == nil || *cmd.Description != "second" {
		t.Fatalf("unexpected GetCommand(dup:3): %+v", cmd)
	}
	if runner.GetCommand("nope") != nil {
		t.Fatalf("expected nil for unknown command")
	}
}

func TestExtensionRunnerReplaceableConflictFiltering(t *testing.T) {
	custom := terMakeExt("/user/custom-mcp.ts")
	custom.Commands["mcp"] = RegisteredCommand{Name: "mcp", Description: terStrPtr("user mcp")}

	builtinMcp := terMakeExt("builtin:mcp")
	builtinMcp.Replaceable = true
	builtinMcp.Commands["mcp"] = RegisteredCommand{Name: "mcp", Description: terStrPtr("builtin mcp")}

	builtinLlama := terMakeExt("builtin:llama")
	builtinLlama.Replaceable = true
	builtinLlama.Commands["llama"] = RegisteredCommand{Name: "llama", Description: terStrPtr("builtin llama")}

	runner := NewExtensionRunner([]Extension{custom, builtinMcp, builtinLlama}, nil, t.TempDir(), nil, nil)
	defer runner.Shutdown()

	paths := runner.GetExtensionPaths()
	if len(paths) != 2 || paths[0] != custom.Path || paths[1] != builtinLlama.Path {
		t.Fatalf("expected custom and builtin:llama only, got %v", paths)
	}
	if cmd := runner.GetCommand("mcp"); cmd == nil || *cmd.Description != "user mcp" {
		t.Fatalf("expected user mcp command, got %+v", cmd)
	}
	if cmd := runner.GetCommand("llama"); cmd == nil || *cmd.Description != "builtin llama" {
		t.Fatalf("expected builtin llama command, got %+v", cmd)
	}
}

func TestExtensionRunnerRenderersAndMarkdownTransformers(t *testing.T) {
	ext1 := terMakeExt("/ext/r1.ts")
	ext1.MessageRenderers["custom-msg"] = func(msg *CustomMessage, _ MessageRenderOptions, _ *theme.Theme) tui.Component {
		return terFakeComp{label: "r1"}
	}
	ext1.EntryRenderers["custom-entry"] = func(entry *CustomEntry, _ EntryRenderOptions, _ *theme.Theme) tui.Component {
		return terFakeComp{label: "e1"}
	}
	ext1.MarkdownTransformer = &MarkdownTransformerRegistration{
		MessageTypes: map[MarkdownMessageType]struct{}{MarkdownMessageTypeAssistant: {}},
		Transformer: func(md string, _ MarkdownTransformContext) string {
			return md + "[A]"
		},
	}
	ext1.ToolRenderers = []ToolRendererResolver{
		func(name string, next func() *ToolRenderers) *ToolRenderers {
			base := next()
			if name == "bash" {
				return &ToolRenderers{
					RenderCall: func(_ *jsonx.Object, _ *theme.Theme, _ ToolRenderContext) tui.Component {
						return terFakeComp{label: "ext1-bash"}
					},
				}
			}
			return base
		},
	}

	ext2 := terMakeExt("/ext/r2.ts")
	ext2.MarkdownTransformer = &MarkdownTransformerRegistration{
		MessageTypes: map[MarkdownMessageType]struct{}{MarkdownMessageTypeUser: {}},
		Transformer: func(md string, _ MarkdownTransformContext) string {
			return md + "[U]"
		},
	}

	runner := NewExtensionRunner([]Extension{ext1, ext2}, nil, t.TempDir(), nil, nil)
	defer runner.Shutdown()

	if r := runner.GetMessageRenderer("custom-msg"); r == nil {
		t.Fatalf("expected message renderer")
	}
	if r := runner.GetEntryRenderer("custom-entry"); r == nil {
		t.Fatalf("expected entry renderer")
	}

	transformers := runner.GetMarkdownTransformers()
	if len(transformers) != 2 {
		t.Fatalf("expected 2 markdown transformers, got %d", len(transformers))
	}
	asstOut := "hi"
	for _, tf := range transformers {
		asstOut = tf(asstOut, MarkdownTransformContext{MessageType: MarkdownMessageTypeAssistant})
	}
	if asstOut != "hi[A]" {
		t.Fatalf("expected hi[A], got %q", asstOut)
	}
	userOut := "hi"
	for _, tf := range transformers {
		userOut = tf(userOut, MarkdownTransformContext{MessageType: MarkdownMessageTypeUser})
	}
	if userOut != "hi[U]" {
		t.Fatalf("expected hi[U], got %q", userOut)
	}

	resolvedBash := runner.ResolveToolRenderers("bash", func() *ToolRenderers {
		return &ToolRenderers{}
	})
	if resolvedBash == nil || resolvedBash.RenderCall == nil {
		t.Fatalf("expected resolved bash renderer")
	}
	comp := resolvedBash.RenderCall(nil, nil, ToolRenderContext{})
	if fc, ok := comp.(terFakeComp); !ok || fc.label != "ext1-bash" {
		t.Fatalf("unexpected resolved tool component: %#v", comp)
	}
	resolvedRead := runner.ResolveToolRenderers("read", func() *ToolRenderers {
		return &ToolRenderers{
			RenderCall: func(_ *jsonx.Object, _ *theme.Theme, _ ToolRenderContext) tui.Component {
				return terFakeComp{label: "base-read"}
			},
		}
	})
	if fc, ok := resolvedRead.RenderCall(nil, nil, ToolRenderContext{}).(terFakeComp); !ok || fc.label != "base-read" {
		t.Fatalf("expected fallback to base renderer, got %#v", resolvedRead)
	}
}

func TestExtensionRunnerBindCoreMcpProvidersShutdownAndUI(t *testing.T) {
	ctx := context.Background()
	rt := CreateExtensionRuntime()
	rt.PendingProviderRegistrations = append(rt.PendingProviderRegistrations, PendingProviderRegistration{
		Name:          "p-ok",
		ExtensionPath: "/ext/p.ts",
	})
	rt.PendingVirtualModelRegistrations = append(rt.PendingVirtualModelRegistrations, PendingVirtualModelRegistration{
		Definition:    VirtualModelDefinition{Provider: "virt", Id: "m1"},
		ExtensionPath: "/ext/v.ts",
	})
	rt.McpServers.Register(RegisteredMcpServer{
		Name:          "unhandled-mcp",
		Config:        McpServerConfig{Command: terStrPtr("echo")},
		ExtensionPath: "/ext/mcp.ts",
	})

	var cleanupOrder []string
	erRegisterRuntimeCleanup(rt, func() {
		cleanupOrder = append(cleanupOrder, "runtime-vm")
	})

	ext := terMakeExt("/ext/ui.ts")
	var promptEvents []string
	ext.Handlers["ui_prompt_start"] = []any{
		ExtensionHandler[UIPromptStartEvent, NoResult](func(_ context.Context, ev *UIPromptStartEvent, _ ExtensionContext) (*NoResult, error) {
			promptEvents = append(promptEvents, "start:"+string(ev.Kind))
			return nil, nil
		}),
	}
	ext.Handlers["ui_prompt_end"] = []any{
		ExtensionHandler[UIPromptEndEvent, NoResult](func(_ context.Context, ev *UIPromptEndEvent, _ ExtensionContext) (*NoResult, error) {
			promptEvents = append(promptEvents, "end:"+string(ev.Kind))
			return nil, nil
		}),
	}

	runner := NewExtensionRunner([]Extension{ext}, rt, t.TempDir(), nil, nil)
	var extErrors []ExtensionError
	unsubErr := runner.OnError(func(err ExtensionError) {
		extErrors = append(extErrors, err)
	})
	defer unsubErr()

	var registeredProviders []string
	var registeredVirtual []string
	var hostShutdownCalled bool
	runner.BindCore(
		ExtensionActions{},
		ExtensionContextActions{
			Shutdown: func() {
				hostShutdownCalled = true
				cleanupOrder = append(cleanupOrder, "host-shutdown")
			},
		},
		&ExtensionProviderActions{
			RegisterProvider: func(name string, _ ProviderConfig) {
				registeredProviders = append(registeredProviders, name)
			},
			RegisterVirtualModel: func(def VirtualModelDefinition) {
				registeredVirtual = append(registeredVirtual, def.Provider+"/"+def.Id)
			},
		},
	)

	if len(registeredProviders) != 1 || registeredProviders[0] != "p-ok" {
		t.Fatalf("expected pending provider flush, got %v", registeredProviders)
	}
	if len(registeredVirtual) != 1 || registeredVirtual[0] != "virt/m1" {
		t.Fatalf("expected pending virtual model flush, got %v", registeredVirtual)
	}

	// ReportUnhandledMcpServers only emits once per server name.
	runner.ReportUnhandledMcpServers()
	runner.ReportUnhandledMcpServers()
	if len(extErrors) != 1 || extErrors[0].Event != "register_mcp_server" {
		t.Fatalf("expected 1 unhandled MCP error, got %+v", extErrors)
	}

	// Registering another MCP server post-BindCore triggers listener -> ReportUnhandledMcpServers.
	rt.McpServers.Register(RegisteredMcpServer{
		Name:          "second-mcp",
		Config:        McpServerConfig{Command: terStrPtr("cat")},
		ExtensionPath: "/ext/mcp2.ts",
	})
	if len(extErrors) != 2 {
		t.Fatalf("expected change listener to report second MCP server, got %+v", extErrors)
	}

	// UI prompt wrapping emits ui_prompt_start / ui_prompt_end only on outermost prompt.
	if runner.HasUI() {
		t.Fatalf("expected HasUI false before SetUIContext")
	}
	var mockUI terMockUIContext
	mockUI = terMockUIContext{
		onSelect: func(c context.Context, title string, options []string) (*string, error) {
			if title == "outer" {
				_, _ = runner.GetUIContext().Confirm(c, "inner", "msg")
			}
			res := options[0]
			return &res, nil
		},
	}
	runner.SetUIContext(&mockUI, ExtensionModeTui)
	if !runner.HasUI() {
		t.Fatalf("expected HasUI true after SetUIContext")
	}
	sel, err := runner.GetUIContext().Select(ctx, "outer", []string{"a", "b"})
	if err != nil || sel == nil || *sel != "a" {
		t.Fatalf("unexpected select result: %v, %v", sel, err)
	}
	if len(promptEvents) != 2 || promptEvents[0] != "start:select" || promptEvents[1] != "end:select" {
		t.Fatalf("unexpected prompt events for nested prompts: %v", promptEvents)
	}

	runner.RegisterCleanup(func() {
		cleanupOrder = append(cleanupOrder, "runner-hook")
	})
	runner.Shutdown()
	runner.Shutdown() // idempotent cleanup hooks
	if !hostShutdownCalled {
		t.Fatalf("expected host shutdown handler to run")
	}
	if len(cleanupOrder) < 3 || cleanupOrder[0] != "runtime-vm" || cleanupOrder[1] != "runner-hook" || cleanupOrder[2] != "host-shutdown" {
		t.Fatalf("unexpected cleanup order: %v", cleanupOrder)
	}
}

func TestExtensionRunnerEventDispatchMergingAndIsolation(t *testing.T) {
	ctx := context.Background()

	t.Run("Emit snapshot isolation, session_before cancel, and panic/error isolation", func(t *testing.T) {
		var calls []string
		ext := terMakeExt("/ext/events.ts")
		var stopB func()
		ext.Handlers["agent_end"] = []any{
			ExtensionHandler[AgentEndEvent, NoResult](func(_ context.Context, _ *AgentEndEvent, _ ExtensionContext) (*NoResult, error) {
				calls = append(calls, "A")
				if stopB != nil {
					stopB()
				}
				return nil, nil
			}),
			ExtensionHandler[AgentEndEvent, NoResult](func(_ context.Context, _ *AgentEndEvent, _ ExtensionContext) (*NoResult, error) {
				calls = append(calls, "B")
				panic("handler boom")
			}),
		}
		stopB = func() {
			ext.Handlers["agent_end"] = ext.Handlers["agent_end"][:1]
		}
		ext.Handlers["session_before_switch"] = []any{
			ExtensionHandler[SessionBeforeSwitchEvent, SessionBeforeSwitchResult](func(_ context.Context, _ *SessionBeforeSwitchEvent, _ ExtensionContext) (*SessionBeforeSwitchResult, error) {
				return &SessionBeforeSwitchResult{Cancel: true}, nil
			}),
			ExtensionHandler[SessionBeforeSwitchEvent, SessionBeforeSwitchResult](func(_ context.Context, _ *SessionBeforeSwitchEvent, _ ExtensionContext) (*SessionBeforeSwitchResult, error) {
				t.Fatalf("expected early short-circuit on cancel=true")
				return nil, nil
			}),
		}

		runner := NewExtensionRunner([]Extension{ext}, nil, t.TempDir(), nil, nil)
		defer runner.Shutdown()

		var errs []ExtensionError
		runner.OnError(func(err ExtensionError) { errs = append(errs, err) })

		_ = runner.Emit(ctx, AgentEndEvent{Type: "agent_end"})
		if len(calls) != 2 || calls[0] != "A" || calls[1] != "B" {
			t.Fatalf("expected snapshot to deliver to A and B, got %v", calls)
		}
		if len(errs) != 1 || errs[0].Error != "handler boom" || errs[0].Stack == nil {
			t.Fatalf("expected isolated panic error with stack, got %+v", errs)
		}

		swRes := runner.Emit(ctx, SessionBeforeSwitchEvent{Type: "session_before_switch", Reason: "new"})
		if sr, ok := swRes.(*SessionBeforeSwitchResult); !ok || !sr.Cancel {
			t.Fatalf("expected Cancel=true result, got %#v", swRes)
		}
	})

	t.Run("EmitContext restores system messages and EmitBeforeProviderHeaders mutates in place", func(t *testing.T) {
		ext := terMakeExt("/ext/ctx.ts")
		sysText := "sys-prompt"
		ext.Handlers["context"] = []any{
			ExtensionHandler[ContextEvent, ContextEventResult](func(_ context.Context, ev *ContextEvent, _ ExtensionContext) (*ContextEventResult, error) {
				for _, m := range ev.Messages {
					if m.GetRole() == "system" {
						t.Fatalf("context handler must not see system messages")
					}
				}
				return &ContextEventResult{Messages: ev.Messages[:1]}, nil
			}),
		}
		ext.Handlers["before_provider_headers"] = []any{
			ExtensionHandler[BeforeProviderHeadersEvent, NoResult](func(_ context.Context, ev *BeforeProviderHeadersEvent, _ ExtensionContext) (*NoResult, error) {
				ev.Headers.Set("X-Ext", terStrPtr("added"))
				return nil, nil
			}),
		}

		runner := NewExtensionRunner([]Extension{ext}, nil, t.TempDir(), nil, nil)
		defer runner.Shutdown()

		inMsgs := []agent.AgentMessage{
			&ai.SystemMessage{Role: "system", Content: ai.SystemMessageContent{Text: &sysText}},
			&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: terStrPtr("u1")}},
			&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: terStrPtr("u2")}},
		}
		outMsgs := runner.EmitContext(ctx, inMsgs)
		if len(outMsgs) != 2 || outMsgs[0].GetRole() != "system" || outMsgs[1].GetRole() != "user" {
			t.Fatalf("expected leading system message restored + 1 user message, got %+v", outMsgs)
		}

		headers := omap.NewMap[string, *string]()
		headers.Set("User-Agent", terStrPtr("pi"))
		gotHeaders := runner.EmitBeforeProviderHeaders(ctx, headers)
		if v, ok := gotHeaders.Get("X-Ext"); !ok || v == nil || *v != "added" {
			t.Fatalf("expected X-Ext header, got %v", gotHeaders.Keys())
		}
	})

	t.Run("EmitBeforeAgentStart updates live getSystemPrompt and aggregates messages", func(t *testing.T) {
		var secondHandlerSawPrompt string
		ext1 := terMakeExt("/ext/b1.ts")
		ext1.Handlers["before_agent_start"] = []any{
			ExtensionHandler[BeforeAgentStartEvent, BeforeAgentStartEventResult](func(_ context.Context, _ *BeforeAgentStartEvent, _ ExtensionContext) (*BeforeAgentStartEventResult, error) {
				return &BeforeAgentStartEventResult{
					SystemPrompt: terStrPtr("forced-by-ext1"),
					Message:      &BeforeAgentStartCustomMessage{CustomType: "note", Content: "m1", Display: true},
				}, nil
			}),
		}
		ext2 := terMakeExt("/ext/b2.ts")
		ext2.Handlers["before_agent_start"] = []any{
			ExtensionHandler[BeforeAgentStartEvent, BeforeAgentStartEventResult](func(_ context.Context, ev *BeforeAgentStartEvent, ectx ExtensionContext) (*BeforeAgentStartEventResult, error) {
				secondHandlerSawPrompt = ectx.GetSystemPrompt()
				if ev.SystemPrompt != secondHandlerSawPrompt {
					t.Fatalf("event.SystemPrompt %q != ctx.GetSystemPrompt() %q", ev.SystemPrompt, secondHandlerSawPrompt)
				}
				return nil, nil
			}),
		}
		runner := NewExtensionRunner([]Extension{ext1, ext2}, nil, t.TempDir(), nil, nil)
		defer runner.Shutdown()

		res := runner.EmitBeforeAgentStart(ctx, BeforeAgentStartTrigger{MessageType: "user", Prompt: "hello"}, BuildSystemPromptOptions{Cwd: t.TempDir()})
		if secondHandlerSawPrompt != "forced-by-ext1" {
			t.Fatalf("expected second handler to see forced system prompt, got %q", secondHandlerSawPrompt)
		}
		if len(res.Messages) != 1 || res.Messages[0].CustomType != "note" {
			t.Fatalf("unexpected injected messages: %+v", res.Messages)
		}
	})

	t.Run("EmitToolCall, EmitToolResult, EmitMessageEnd, EmitUserBash, EmitInput, EmitBoundary, EmitProjectTrustEvent", func(t *testing.T) {
		ext := terMakeExt("/ext/all.ts")
		ext.Handlers["tool_call"] = []any{
			ExtensionHandler[ToolCallEvent, ToolCallEventResult](func(_ context.Context, ev *ToolCallEvent, _ ExtensionContext) (*ToolCallEventResult, error) {
				if ev.ToolName == "bash" {
					return &ToolCallEventResult{Block: terBoolPtr(true), Reason: terStrPtr("blocked")}, nil
				}
				return nil, errors.New("tool_call non-fatal err")
			}),
		}
		ext.Handlers["tool_result"] = []any{
			ExtensionHandler[ToolResultEvent, ToolResultEventResult](func(_ context.Context, _ *ToolResultEvent, _ ExtensionContext) (*ToolResultEventResult, error) {
				return &ToolResultEventResult{
					Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "rewritten"}},
					IsError: terBoolPtr(true),
				}, nil
			}),
		}
		ext.Handlers["message_end"] = []any{
			ExtensionHandler[MessageEndEvent, MessageEndEventResult](func(_ context.Context, _ *MessageEndEvent, _ ExtensionContext) (*MessageEndEventResult, error) {
				return &MessageEndEventResult{
					Message: &ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: terStrPtr("wrong-role")}},
				}, nil
			}),
		}
		ext.Handlers["user_bash"] = []any{
			ExtensionHandler[UserBashEvent, UserBashEventResult](func(_ context.Context, ev *UserBashEvent, _ ExtensionContext) (*UserBashEventResult, error) {
				switch ev.Command {
				case "throw":
					return nil, errors.New("routing failed")
				case "invalid":
					return &UserBashEventResult{}, nil
				case "ops":
					return &UserBashEventResult{Operations: &terMockBashOps{}}, nil
				default:
					return nil, nil
				}
			}),
		}
		ext.Handlers["input"] = []any{
			ExtensionHandler[InputEvent, InputEventResult](func(_ context.Context, ev *InputEvent, _ ExtensionContext) (*InputEventResult, error) {
				if ev.Text == "stop" {
					return &InputEventResult{Action: "handled"}, nil
				}
				return &InputEventResult{Action: "transform", Text: ev.Text + "!"}, nil
			}),
		}
		ext.Handlers["turn_end"] = []any{
			ExtensionHandler[TurnEndEvent, BoundaryResult](func(_ context.Context, ev *TurnEndEvent, _ ExtensionContext) (*BoundaryResult, error) {
				return &BoundaryResult{
					Entries:  append(ev.Entries, CustomEntryDraft{Type: "custom", CustomType: "tag", Data: 1}),
					Continue: terBoolPtr(true),
				}, nil
			}),
		}
		ext.Handlers["project_trust"] = []any{
			ProjectTrustHandler(func(_ ProjectTrustEvent, _ ProjectTrustContext) (ProjectTrustEventResult, error) {
				return ProjectTrustEventResult{Trusted: ProjectTrustEventDecisionUndecided}, nil
			}),
			ProjectTrustHandler(func(_ ProjectTrustEvent, _ ProjectTrustContext) (ProjectTrustEventResult, error) {
				return ProjectTrustEventResult{Trusted: ProjectTrustEventDecisionYes, Remember: terBoolPtr(true)}, nil
			}),
		}

		runner := NewExtensionRunner([]Extension{ext}, nil, t.TempDir(), nil, nil)
		defer runner.Shutdown()

		var errs []ExtensionError
		runner.OnError(func(err ExtensionError) { errs = append(errs, err) })

		tcRes := runner.EmitToolCall(ctx, ToolCallEvent{ToolName: "bash"})
		if tcRes == nil || tcRes.Block == nil || !*tcRes.Block {
			t.Fatalf("expected tool_call block, got %+v", tcRes)
		}
		_ = runner.EmitToolCall(ctx, ToolCallEvent{ToolName: "read"})
		if len(errs) == 0 || errs[len(errs)-1].Event != "tool_call" {
			t.Fatalf("expected isolated tool_call error, got %+v", errs)
		}

		trRes := runner.EmitToolResult(ctx, ToolResultEvent{ToolName: "read", StructuredContent: map[string]any{"x": 1}})
		if trRes == nil || len(trRes.Content) != 1 || trRes.StructuredContent != nil || !*trRes.IsError {
			t.Fatalf("unexpected EmitToolResult: %+v", trRes)
		}

		if me := runner.EmitMessageEnd(ctx, MessageEndEvent{Message: &ai.AssistantMessage{Role: "assistant"}}); me != nil {
			t.Fatalf("expected wrong-role message_end replacement to be rejected, got %+v", me)
		}

		if res := runner.EmitUserBash(ctx, UserBashEvent{Command: "throw"}); res == nil || res.Result == nil || !res.Result.Cancelled {
			t.Fatalf("expected EmitUserBash fail-closed result on error, got %+v", res)
		}
		if _, err := runner.EmitUserBashWithError(ctx, UserBashEvent{Command: "invalid"}); err == nil {
			t.Fatalf("expected EmitUserBashWithError to reject empty UserBashEventResult")
		}
		if res, err := runner.EmitUserBashWithError(ctx, UserBashEvent{Command: "ops"}); err != nil || res == nil || res.Operations == nil {
			t.Fatalf("expected EmitUserBashWithError ops result, got %+v, %v", res, err)
		}

		if inRes := runner.EmitInput(ctx, "hello", nil, InputSourceInteractive); inRes.Action != "transform" || inRes.Text != "hello!" {
			t.Fatalf("unexpected EmitInput transform: %+v", inRes)
		}
		if inRes := runner.EmitInput(ctx, "stop", nil, InputSourceInteractive); inRes.Action != "handled" {
			t.Fatalf("unexpected EmitInput handled: %+v", inRes)
		}

		bRes, err := runner.EmitBoundary(ctx, BoundaryBaseEvent{Type: "turn_end", Outcome: AgentActivityCompleted}, func(entries []SessionBoundaryDraft) (BoundaryContextPreview, error) {
			return BoundaryContextPreview{CanContinue: true}, nil
		})
		if err != nil || !bRes.Valid || !bRes.Continue || len(bRes.Entries) != 1 {
			t.Fatalf("unexpected EmitBoundary result: %+v, err=%v", bRes, err)
		}

		trustRes, trustErrs := EmitProjectTrustEvent(&LoadExtensionsResult{Extensions: []Extension{ext}}, ProjectTrustEvent{Cwd: "/tmp"}, ProjectTrustContext{})
		if len(trustErrs) != 0 || trustRes == nil || trustRes.Trusted != ProjectTrustEventDecisionYes {
			t.Fatalf("unexpected EmitProjectTrustEvent: %+v, errs=%+v", trustRes, trustErrs)
		}
	})
}

func TestExtensionRunnerConcurrencyRace(t *testing.T) {
	ext := terMakeExt("/ext/race.ts")
	var count atomic.Int64
	ext.Handlers["agent_start"] = []any{
		ExtensionHandler[AgentStartEvent, NoResult](func(_ context.Context, _ *AgentStartEvent, ectx ExtensionContext) (*NoResult, error) {
			_ = ectx.HasUI()
			_ = ectx.Cwd()
			count.Add(1)
			return nil, nil
		}),
	}
	runner := NewExtensionRunner([]Extension{ext}, nil, t.TempDir(), nil, nil)
	defer runner.Shutdown()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				unsub := runner.OnError(func(ExtensionError) {})
				_ = runner.Emit(context.Background(), AgentStartEvent{Type: "agent_start"})
				_ = runner.GetExtensionPaths()
				_ = runner.GetRegisteredCommands()
				unsub()
			}
		}()
	}
	wg.Wait()
	if count.Load() != 200 {
		t.Fatalf("expected 200 handler invocations, got %d", count.Load())
	}
}
