package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/tui"
)

func teqjsStrPtr(s string) *string { return &s }

func teqjsJSONObject(m map[string]any) *jsonx.Object {
	raw, _ := jsonx.Marshal(m)
	v, _ := jsonx.Parse(raw)
	if o, ok := v.(*jsonx.Object); ok {
		return o
	}
	return jsonx.NewObject()
}

type teqjsMockUI struct {
	mu            sync.Mutex
	selectChoice  string
	confirmAns    bool
	inputText     string
	editorText    string
	statuses      map[string]string
	notifications []string
	title         string
}

func (m *teqjsMockUI) Select(_ context.Context, _ string, _ []string, _ ...*ExtensionUIDialogOptions) (*string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.selectChoice
	return &s, nil
}
func (m *teqjsMockUI) Confirm(_ context.Context, _, _ string, _ ...*ExtensionUIDialogOptions) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.confirmAns, nil
}
func (m *teqjsMockUI) Input(_ context.Context, _ string, _ *string, _ ...*ExtensionUIDialogOptions) (*string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.inputText
	return &s, nil
}
func (m *teqjsMockUI) Notify(message string, notifyType ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	nt := "info"
	if len(notifyType) > 0 && notifyType[0] != "" {
		nt = notifyType[0]
	}
	m.notifications = append(m.notifications, nt+":"+message)
}
func (m *teqjsMockUI) OnTerminalInput(_ TerminalInputHandler) func() { return func() {} }
func (m *teqjsMockUI) SetStatus(key string, text *string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.statuses == nil {
		m.statuses = map[string]string{}
	}
	if text == nil {
		delete(m.statuses, key)
	} else {
		m.statuses[key] = *text
	}
}
func (m *teqjsMockUI) SetWorkingMessage(...string)                                 {}
func (m *teqjsMockUI) SetWorkingVisible(bool)                                      {}
func (m *teqjsMockUI) SetWorkingIndicator(*WorkingIndicatorOptions)                {}
func (m *teqjsMockUI) SetHiddenThinkingLabel(...string)                            {}
func (m *teqjsMockUI) SetWidgetLines(string, []string, ...*ExtensionWidgetOptions) {}
func (m *teqjsMockUI) SetWidgetFactory(string, func(tui.TUI, *theme.Theme) tui.Component, ...*ExtensionWidgetOptions) {
}
func (m *teqjsMockUI) SetFooter(func(tui.TUI, *theme.Theme, ReadonlyFooterDataProvider) tui.Component) {
}
func (m *teqjsMockUI) SetHeader(func(tui.TUI, *theme.Theme) tui.Component) {}
func (m *teqjsMockUI) SetTitle(title string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.title = title
}
func (m *teqjsMockUI) Custom(
	ctx context.Context,
	factory func(tui.TUI, *theme.Theme, *tui.KeybindingsManager, func(any)) (tui.Component, error),
	_ ...*ExtensionCustomOptions,
) (any, error) {
	doneCh := make(chan any, 1)
	comp, err := factory(nil, nil, nil, func(v any) {
		select {
		case doneCh <- v:
		default:
		}
	})
	if err != nil {
		return nil, err
	}
	_ = comp.Render(40)
	if ih, ok := comp.(interface{ HandleInput(string) }); ok {
		ih.HandleInput("confirm")
	}
	select {
	case v := <-doneCh:
		return v, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(2 * time.Second):
		return nil, context.DeadlineExceeded
	}
}
func (m *teqjsMockUI) PasteToEditor(string) {}
func (m *teqjsMockUI) SetEditorText(text string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.editorText = text
}
func (m *teqjsMockUI) GetEditorText() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.editorText
}
func (m *teqjsMockUI) Editor(_ context.Context, _ string, _ ...string) (*string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.editorText
	return &s, nil
}
func (m *teqjsMockUI) AddAutocompleteProvider(AutocompleteProviderFactory) {}
func (m *teqjsMockUI) SetEditorComponent(EditorFactory)                    {}
func (m *teqjsMockUI) GetEditorComponent() EditorFactory                   { return nil }
func (m *teqjsMockUI) Theme() *theme.Theme                                 { return nil }
func (m *teqjsMockUI) GetAllThemes() []ThemeInfo                           { return nil }
func (m *teqjsMockUI) GetTheme(string) *theme.Theme                        { return nil }
func (m *teqjsMockUI) SetThemeByName(string) SetThemeResult                { return SetThemeResult{Success: true} }
func (m *teqjsMockUI) SetTheme(*theme.Theme) SetThemeResult                { return SetThemeResult{Success: true} }
func (m *teqjsMockUI) GetToolsExpanded() bool                              { return false }
func (m *teqjsMockUI) SetToolsExpanded(bool)                               {}

func teqjsLoadTS(t *testing.T, files map[string]string, entries ...string) (*ExtensionRunner, LoadExtensionsResult, string) {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var paths []string
	for _, e := range entries {
		paths = append(paths, filepath.Join(dir, e))
	}
	res := LoadExtensions(context.Background(), paths, dir)
	if len(res.Errors) != 0 {
		t.Fatalf("LoadExtensions failed: %+v", res.Errors)
	}
	runner := NewExtensionRunner(res.Extensions, res.Runtime, dir, nil, nil)
	t.Cleanup(func() {
		runner.Shutdown()
	})
	return runner, res, dir
}

func TestQuickJSTypeScriptToolAndTypebox(t *testing.T) {
	ctx := context.Background()
	runner, _, _ := teqjsLoadTS(t, map[string]string{
		"tool.ts": `
import type { ExtensionAPI } from "@mariozechner/pi-coding-agent";
import { StringEnum } from "@earendil-works/pi-ai";
import { Type } from "@sinclair/typebox";

interface ExecSpec {
  tag: string;
  count: number;
}

export default function (pi: ExtensionAPI) {
  pi.registerTool({
    name: "greet_exec",
    label: "Greet & Exec",
    description: "Validates TypeBox params, streams updates, runs pi.exec",
    parameters: Type.Object({
      name: Type.String(),
      mode: StringEnum(["fast", "thorough"] as const),
      count: Type.Optional(Type.Integer({ minimum: 1 })),
    }),
    async execute(_toolCallId, params, _signal, onUpdate, _ctx) {
      onUpdate?.({ content: [{ type: "text", text: "working:" + params.name }] });
      const res = await pi.exec("sh", ["-c", "printf 'exec-ok:%s:%s' '" + params.name + "' '" + params.mode + "'"]);
      const spec: ExecSpec = { tag: res.stdout, count: params.count ?? 1 };
      return {
        content: [{ type: "text", text: spec.tag + "#" + String(spec.count) }],
        details: spec,
      };
    },
  });
}
`,
	}, "tool.ts")

	def := runner.GetToolDefinition("greet_exec")
	if def == nil {
		t.Fatalf("greet_exec not registered")
	}
	if def.Parameters == nil {
		t.Fatalf("expected non-nil TypeBox parameters schema")
	}

	// Verify ai.ValidateToolArguments validates and rejects bad enum/missing required.
	aiTool := &ai.Tool{
		Name:        def.Name,
		Description: def.Description,
		Parameters:  def.Parameters,
	}
	validArgs, err := ai.ValidateToolArguments(aiTool, &ai.ToolCall{
		Name:      "greet_exec",
		Arguments: teqjsJSONObject(map[string]any{"name": "ada", "mode": "fast", "count": 2}),
	})
	if err != nil {
		t.Fatalf("expected valid args to validate: %v", err)
	}
	if _, err := ai.ValidateToolArguments(aiTool, &ai.ToolCall{
		Name:      "greet_exec",
		Arguments: teqjsJSONObject(map[string]any{"name": "ada", "mode": "invalid-enum"}),
	}); err == nil {
		t.Fatalf("expected StringEnum validation error for invalid mode")
	}

	var updates []string
	var updMu sync.Mutex
	onUpd := func(partial agent.AgentToolResult) {
		updMu.Lock()
		defer updMu.Unlock()
		if len(partial.Content) > 0 {
			if tc, ok := partial.Content[0].(*ai.TextContent); ok {
				updates = append(updates, tc.Text)
			}
		}
	}

	var paramsObj *jsonx.Object
	switch v := validArgs.(type) {
	case *jsonx.Object:
		paramsObj = v
	case map[string]any:
		paramsObj = teqjsJSONObject(v)
	default:
		t.Fatalf("unexpected validated args type %T", validArgs)
	}
	out, err := def.Execute(ctx, "call-1", paramsObj, onUpd, runner.CreateToolContext("call-1", ctx))
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if len(out.Content) == 0 {
		t.Fatalf("expected non-empty content")
	}
	gotText := out.Content[0].(*ai.TextContent).Text
	if gotText != "exec-ok:ada:fast#2" {
		t.Fatalf("got tool output %q, want %q", gotText, "exec-ok:ada:fast#2")
	}
	// Poll briefly for async update goroutine delivery.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		updMu.Lock()
		n := len(updates)
		updMu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	updMu.Lock()
	if !slices.Contains(updates, "working:ada") {
		t.Fatalf("expected onUpdate partial 'working:ada', got %v", updates)
	}
	updMu.Unlock()
}

func TestQuickJSModuleResolutionAndShims(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			select {
			case <-time.After(2 * time.Second):
				w.WriteHeader(200)
			case <-r.Context().Done():
			}
			return
		}
		if r.Header.Get("X-Custom-Client") != "pi-qjs" {
			http.Error(w, "missing header", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"method":"` + r.Method + `"}`))
	}))
	defer srv.Close()

	t.Setenv("PI_QJS_TEST_ENV", "env-value-42")
	ctx := context.Background()
	runner, _, dir := teqjsLoadTS(t, map[string]string{
		"helper.ts": `
export function formatDigest(s: string): string {
  return "helper:" + s;
}
`,
		"ext.ts": `
import path from "node:path";
import os from "node:os";
import fs from "node:fs";
import fsp from "node:fs/promises";
import crypto from "node:crypto";
import { EventEmitter } from "node:events";
import { Buffer } from "node:buffer";
import { pathToFileURL, fileURLToPath } from "node:url";
import { Type } from "typebox";
import { VERSION } from "@mariozechner/pi-coding-agent";
import { formatDigest } from "./helper.js";

export default function (pi: any) {
  pi.registerTool({
    name: "probe_shims",
    label: "Probe Shims",
    description: "Exercises Node shims, globals, and fetch",
    parameters: Type.Object({ url: Type.String(), dir: Type.String() }),
    async execute(_id: string, params: any) {
      const fpath = path.join(params.dir, "sub", "data.txt");
      fs.mkdirSync(path.dirname(fpath), { recursive: true });
      fs.writeFileSync(fpath, "sync-data");
      const r1 = fs.readFileSync(fpath, "utf8");
      await fsp.writeFile(fpath + ".async", Buffer.from("hello-async").toString("base64"));
      const r2 = Buffer.from(await fsp.readFile(fpath + ".async", "utf8"), "base64").toString("utf8");

      const hash = crypto.createHash("sha256").update("pi").digest("hex");
      const uuid = crypto.randomUUID();
      const ee = new EventEmitter();
      let eeVal = "";
      ee.on("ping", (v: string) => { eeVal = v; });
      ee.emit("ping", "pong");

      const enc = new TextDecoder().decode(new TextEncoder().encode("utf8-ok"));
      const parsedUrl = new URL("https://example.com/a?b=1&c=2");
      const fileRoundtrip = fileURLToPath(pathToFileURL(fpath).href);

      let order: string[] = [];
      queueMicrotask(() => order.push("micro"));
      await new Promise<void>((resolve) => setTimeout(() => { order.push("timer"); resolve(); }, 10));

      const resp = await fetch(params.url, {
        method: "POST",
        headers: { "X-Custom-Client": "pi-qjs", "Content-Type": "application/json" },
        body: JSON.stringify({ hello: "world" }),
      });
      const jsonBody = await resp.json();

      // Verify AbortController aborts fetch cleanly.
      const ac = new AbortController();
      setTimeout(() => ac.abort("stop-now"), 25);
      let fetchAborted = false;
      try {
        await fetch(params.url.replace(/\/$/, "") + "/slow", { signal: ac.signal });
      } catch (_err) {
        fetchAborted = true;
      }

      const payload = {
        helper: formatDigest(VERSION),
        r1,
        r2,
        exists: fs.existsSync(fpath),
        entries: fs.readdirSync(path.dirname(fpath)),
        hashLen: hash.length,
        uuidLen: uuid.length,
        eeVal,
        enc,
        queryB: parsedUrl.searchParams.get("b"),
        fileRoundtrip,
        osTmpLen: os.tmpdir().length > 0,
        envVal: process.env.PI_QJS_TEST_ENV,
        order: order.join(","),
        fetchJson: jsonBody,
        fetchAborted,
      };
      return { content: [{ type: "text", text: JSON.stringify(payload) }] };
    },
  });
}
`,
	}, "ext.ts")

	def := runner.GetToolDefinition("probe_shims")
	if def == nil {
		t.Fatalf("probe_shims not found")
	}
	out, err := def.Execute(ctx, "c1", teqjsJSONObject(map[string]any{
		"url": srv.URL,
		"dir": filepath.Join(dir, "scratch"),
	}), nil, runner.CreateToolContext("c1", ctx))
	if err != nil {
		t.Fatalf("probe_shims execute failed: %v", err)
	}
	var got struct {
		Helper       string         `json:"helper"`
		R1           string         `json:"r1"`
		R2           string         `json:"r2"`
		Exists       bool           `json:"exists"`
		Entries      []string       `json:"entries"`
		HashLen      int            `json:"hashLen"`
		UuidLen      int            `json:"uuidLen"`
		EeVal        string         `json:"eeVal"`
		Enc          string         `json:"enc"`
		QueryB       string         `json:"queryB"`
		FileRound    string         `json:"fileRoundtrip"`
		OsTmpLen     bool           `json:"osTmpLen"`
		EnvVal       string         `json:"envVal"`
		Order        string         `json:"order"`
		FetchJson    map[string]any `json:"fetchJson"`
		FetchAborted bool           `json:"fetchAborted"`
	}
	rawTxt := out.Content[0].(*ai.TextContent).Text
	if err := jsonx.Unmarshal([]byte(rawTxt), &got); err != nil {
		t.Fatalf("unmarshal payload failed (%v): %s", err, rawTxt)
	}
	if !strings.HasPrefix(got.Helper, "helper:") || got.R1 != "sync-data" || got.R2 != "hello-async" ||
		!got.Exists || len(got.Entries) != 2 || got.HashLen != 64 || got.UuidLen != 36 ||
		got.EeVal != "pong" || got.Enc != "utf8-ok" || got.QueryB != "1" ||
		got.EnvVal != "env-value-42" || !got.OsTmpLen || got.Order != "micro,timer" ||
		got.FetchJson["ok"] != true || got.FetchJson["method"] != "POST" || !got.FetchAborted {
		t.Fatalf("unexpected shim probe output: %+v (raw=%s)", got, rawTxt)
	}
}

func TestQuickJSHooksAndEvents(t *testing.T) {
	ctx := context.Background()
	runner, _, _ := teqjsLoadTS(t, map[string]string{
		"hooks.ts": `
export default function (pi: any) {
  pi.on("tool_call", async (event: any) => {
    if (event.toolName === "blocked_tool") {
      return { block: true, reason: "denied by policy" };
    }
    if (event.input && typeof event.input.text === "string") {
      event.input.text = event.input.text + "-mutated";
    }
  });

  pi.on("tool_result", async (event: any) => {
    if (event.toolName === "echo") {
      return {
        content: [{ type: "text", text: "rewritten-result" }],
        isError: false,
      };
    }
  });

  pi.on("input", async (event: any) => {
    if (event.text === "stop") return { action: "handled" };
    if (event.text === "ping") return { action: "transform", text: "pong" };
  });

  pi.on("context", async (event: any) => {
    return {
      messages: event.messages.filter((m: any) => !JSON.stringify(m).includes("drop-me")),
    };
  });

  pi.on("before_provider_headers", async (event: any) => {
    event.headers["X-Ext"] = "added";
    delete event.headers["X-Drop"];
  });

  pi.on("before_agent_start", async (event: any) => {
    return {
      systemPrompt: event.systemPrompt + " +ext",
      message: {
        customType: "ext-note",
        content: "injected",
        display: true,
      },
    };
  });

  pi.on("user_bash", async (event: any) => {
    if (event.command === "override-me") {
      return {
        result: { output: "from-ext", exitCode: 0, cancelled: false, truncated: false },
      };
    }
  });
}
`,
	}, "hooks.ts")

	// 1. tool_call block + input mutation
	blockRes := runner.EmitToolCall(ctx, ToolCallEvent{
		Type:     "tool_call",
		ToolName: "blocked_tool",
		Input:    teqjsJSONObject(map[string]any{"text": "hi"}),
	})
	if blockRes == nil || blockRes.Block == nil || !*blockRes.Block || blockRes.Reason == nil || *blockRes.Reason != "denied by policy" {
		t.Fatalf("expected block result, got res=%+v", blockRes)
	}
	mutEv := ToolCallEvent{
		Type:     "tool_call",
		ToolName: "echo",
		Input:    teqjsJSONObject(map[string]any{"text": "hello"}),
	}
	_ = runner.EmitToolCall(ctx, mutEv)
	if v, _ := mutEv.Input.Get("text"); v != "hello-mutated" {
		t.Fatalf("expected mutated input text 'hello-mutated', got %#v", v)
	}

	// 2. tool_result
	trRes := runner.EmitToolResult(ctx, ToolResultEvent{
		Type:     "tool_result",
		ToolName: "echo",
		Content:  ai.ContentList{&ai.TextContent{Type: "text", Text: "orig"}},
	})
	if trRes == nil || len(trRes.Content) != 1 || trRes.Content[0].(*ai.TextContent).Text != "rewritten-result" {
		t.Fatalf("unexpected tool_result rewrite: %+v", trRes)
	}

	// 3. input handled + transform
	inHandled := runner.EmitInput(ctx, "stop", nil, "interactive")
	if inHandled.Action != "handled" {
		t.Fatalf("expected handled input, got %+v", inHandled)
	}
	inTransform := runner.EmitInput(ctx, "ping", nil, "interactive")
	if inTransform.Action != "transform" || inTransform.Text != "pong" {
		t.Fatalf("expected transform -> 'pong', got %+v", inTransform)
	}

	// 4. context filters drop-me while keeping keep-me
	dropMsg := &ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Blocks: ai.ContentList{&ai.TextContent{Type: "text", Text: "drop-me"}}}}
	keepMsg := &ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Blocks: ai.ContentList{&ai.TextContent{Type: "text", Text: "keep-me"}}}}
	ctxOut := runner.EmitContext(ctx, []agent.AgentMessage{dropMsg, keepMsg})
	if len(ctxOut) != 1 {
		t.Fatalf("expected 1 kept message from EmitContext, got %d (%+v)", len(ctxOut), ctxOut)
	}

	// 5. before_provider_headers in-place mutation
	hdrs := omap.NewMap[string, *string]()
	hdrs.Set("X-Drop", teqjsStrPtr("bye"))
	hdrs.Set("X-Keep", teqjsStrPtr("stay"))
	runner.EmitBeforeProviderHeaders(ctx, hdrs)
	if hdrs.Has("X-Drop") {
		t.Fatalf("expected X-Drop deleted")
	}
	if v, ok := hdrs.Get("X-Ext"); !ok || v == nil || *v != "added" {
		t.Fatalf("expected X-Ext='added', got %v %v", v, ok)
	}
	if v, ok := hdrs.Get("X-Keep"); !ok || v == nil || *v != "stay" {
		t.Fatalf("expected X-Keep preserved")
	}

	// 6. before_agent_start
	basRes := runner.EmitBeforeAgentStart(ctx, BeforeAgentStartTrigger{MessageType: "user", Prompt: "hi"}, BuildSystemPromptOptions{
		CustomPrompt: teqjsStrPtr("base"),
	})
	if len(basRes.Messages) != 1 || basRes.SystemPromptOptions.ForceSystemPrompt == nil || !strings.Contains(*basRes.SystemPromptOptions.ForceSystemPrompt, "+ext") {
		t.Fatalf("unexpected before_agent_start result: %+v", basRes)
	}

	// 7. user_bash
	ubRes := runner.EmitUserBash(ctx, UserBashEvent{Type: "user_bash", Command: "override-me", Cwd: runner.cwd})
	if ubRes == nil || ubRes.Result == nil || ubRes.Result.Output != "from-ext" {
		t.Fatalf("unexpected user_bash result: %+v", ubRes)
	}
}

func TestQuickJSCommandsCompletionsAndUI(t *testing.T) {
	ctx := context.Background()
	runner, res, _ := teqjsLoadTS(t, map[string]string{
		"ui_cmd.ts": `
import { visibleWidth, truncateToWidth } from "@mariozechner/pi-tui";

export default function (pi: any) {
  pi.registerFlag("greet-prefix", {
    description: "Greeting prefix",
    type: "string",
    default: "hello",
  });

  pi.registerCommand("review", {
    description: "Run interactive review",
    getArgumentCompletions(prefix: string) {
      return [
        { value: prefix + "fast", label: "fast", description: "Fast pass" },
        { value: prefix + "deep", label: "deep", description: "Deep pass" },
      ];
    },
    async handler(args: string, ctx: any) {
      await ctx.waitForIdle();
      const picked = await ctx.ui.select("Pick mode", ["a", "b"]);
      const confirmed = await ctx.ui.confirm("Sure?", "Proceed");
      const customVal = await ctx.ui.custom((tui: any, theme: any, _kb: any, done: any) => {
        const line = truncateToWidth("prefix-" + args + "-" + picked, 20, "...");
        return {
          render(_w: number) { return [line + "(" + visibleWidth(line) + ")"]; },
          invalidate() {},
          handleInput(data: string) {
            if (data === "confirm") done("custom:" + line);
          },
        };
      });
      const flagPrefix = pi.getFlag("greet-prefix");
      ctx.ui.setStatus("ext", String(customVal));
      ctx.ui.notify(flagPrefix + ":" + String(confirmed) + ":" + String(customVal), "info");
      pi.appendEntry("review-entry", { customVal, args });
      pi.sendMessage({ customType: "review-msg", content: String(customVal), display: true });
    },
  });
}
`,
	}, "ui_cmd.ts")

	mockUI := &teqjsMockUI{
		selectChoice: "optA",
		confirmAns:   true,
	}
	runner.SetUIContext(mockUI, ExtensionModeTui)

	var waitCalled bool
	runner.BindCommandContext(&ExtensionCommandContextActions{
		WaitForIdle: func(context.Context) error { waitCalled = true; return nil },
	})
	var sentMsgs []CustomMessageInput
	var appended []struct {
		t string
		d any
	}
	runner.BindCore(ExtensionActions{
		SendMessage: func(m CustomMessageInput, _ ...*SendMessageOptions) { sentMsgs = append(sentMsgs, m) },
		AppendEntry: func(ct string, data any) {
			appended = append(appended, struct {
				t string
				d any
			}{ct, data})
		},
	}, ExtensionContextActions{})

	_ = res
	cmd := runner.GetCommand("review")
	if cmd == nil {
		t.Fatalf("review command not found")
	}
	items, err := cmd.GetArgumentCompletions(ctx, "mode-")
	if err != nil || len(items) != 2 || items[0].Value != "mode-fast" {
		t.Fatalf("unexpected completions: %+v err=%v", items, err)
	}

	if err := cmd.Handler(ctx, "arg1", runner.CreateCommandContext()); err != nil {
		t.Fatalf("command handler failed: %v", err)
	}
	if !waitCalled {
		t.Fatalf("expected WaitForIdle called")
	}
	if mockUI.statuses["ext"] != "custom:prefix-arg1-optA" {
		t.Fatalf("unexpected UI status: %#v", mockUI.statuses)
	}
	if len(mockUI.notifications) == 0 || mockUI.notifications[0] != "info:hello:true:custom:prefix-arg1-optA" {
		t.Fatalf("unexpected notifications: %v", mockUI.notifications)
	}
	if len(sentMsgs) != 1 || len(appended) != 1 || appended[0].t != "review-entry" {
		t.Fatalf("expected sent message and appended entry, got sent=%+v appended=%+v", sentMsgs, appended)
	}
}

func TestQuickJSCustomProviderStreaming(t *testing.T) {
	ctx := context.Background()
	runner, res, _ := teqjsLoadTS(t, map[string]string{
		"provider.ts": `
import { createAssistantMessageEventStream } from "@earendil-works/pi-ai";

export default function (pi: any) {
  pi.registerProvider("qjs-custom", {
    baseUrl: "https://custom.local/v1",
    apiKey: "CUSTOM_KEY",
    api: "openai-completions",
    models: [
      {
        id: "mini-1",
        name: "Mini 1",
        reasoning: false,
        input: ["text"],
        cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
        contextWindow: 8192,
        maxTokens: 1024,
      },
    ],
    streamSimple(model: any, _context: any, _options: any) {
      const stream = createAssistantMessageEventStream();
      (async () => {
        const partial: any = {
          role: "assistant",
          content: [{ type: "text", text: "" }],
          api: model.api,
          provider: model.provider,
          model: model.id,
          usage: { input: 1, output: 2, cacheRead: 0, cacheWrite: 0, totalTokens: 3, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } },
          stopReason: "stop",
          timestamp: Date.now(),
        };
        stream.push({ type: "start", partial });
        await new Promise((r) => setTimeout(r, 10));
        partial.content = [{ type: "text", text: "Hello " }];
        stream.push({ type: "text_delta", contentIndex: 0, delta: "Hello ", partial });
        await new Promise((r) => setTimeout(r, 10));
        partial.content = [
          { type: "text", text: "Hello world" },
          { type: "toolCall", id: "tc_1", name: "lookup", arguments: { q: "pi" } },
        ];
        stream.push({ type: "done", reason: "stop", message: partial });
        stream.end(partial);
      })();
      return stream;
    },
  });
}
`,
	}, "provider.ts")
	_ = runner

	if len(res.Runtime.PendingProviderRegistrations) != 1 {
		t.Fatalf("expected 1 pending provider registration, got %d", len(res.Runtime.PendingProviderRegistrations))
	}
	reg := res.Runtime.PendingProviderRegistrations[0]
	if reg.Name != "qjs-custom" || reg.Config.StreamSimple == nil {
		t.Fatalf("unexpected provider registration: %+v", reg)
	}
	model := &ai.Model{Id: "mini-1", Api: "openai-completions", Provider: "qjs-custom"}
	stream := reg.Config.StreamSimple(ctx, model, &ai.TranscriptContext{}, &ai.SimpleStreamOptions{})

	var eventTypes []string
	for ev := range stream.All() {
		switch x := ev.(type) {
		case *ai.AssistantMessageEventStart:
			eventTypes = append(eventTypes, x.Type)
		case *ai.AssistantMessageEventTextDelta:
			eventTypes = append(eventTypes, x.Type+":"+x.Delta)
		case *ai.AssistantMessageEventDone:
			eventTypes = append(eventTypes, x.Type)
		}
	}
	finalMsg, err := stream.Result(ctx)
	if err != nil || finalMsg == nil || finalMsg.StopReason != ai.StopReasonStop || len(finalMsg.Content) != 2 {
		t.Fatalf("unexpected final streamed message: %+v err=%v (events=%v)", finalMsg, err, eventTypes)
	}
	if !slices.Contains(eventTypes, "start") || !slices.Contains(eventTypes, "text_delta:Hello ") || !slices.Contains(eventTypes, "done") {
		t.Fatalf("expected start/text_delta/done stream events, got %v", eventTypes)
	}
}

func TestQuickJSEventBusAndErrorIsolation(t *testing.T) {
	ctx := context.Background()
	runner, _, _ := teqjsLoadTS(t, map[string]string{
		"a_bus_and_throw.ts": `
export default function (pi: any) {
  const seen: string[] = [];
  pi.events.on("shared", (d: any) => {
    seen.push("a:" + String(d));
    if (d === "first") {
      // Re-entrant emit from inside listener on same VM!
      pi.events.emit("shared", "reentrant");
    }
  });

  pi.on("tool_call", () => {
    throw new Error("sync-boom");
  });

  pi.on("input", async () => {
    throw new Error("async-reject");
  });

  pi.registerTool({
    name: "trigger_bus",
    label: "Trigger Bus",
    description: "emits on event bus and reads log",
    parameters: { type: "object", properties: {} },
    async execute() {
      pi.events.emit("shared", "first");
      return { content: [{ type: "text", text: seen.join(",") }] };
    },
  });
}
`,
		"b_peer.ts": `
export default function (pi: any) {
  let peerLog: string[] = [];
  pi.events.on("shared", (d: any) => {
    peerLog.push("b:" + String(d));
  });

  pi.on("tool_call", () => {
    return { block: true, reason: "b-still-ran" };
  });

  pi.registerTool({
    name: "read_peer_log",
    label: "Read Peer",
    description: "reads peer log",
    parameters: { type: "object", properties: {} },
    async execute() {
      return { content: [{ type: "text", text: peerLog.join(",") }] };
    },
  });
}
`,
	}, "a_bus_and_throw.ts", "b_peer.ts")

	var recordedErrs []ExtensionError
	var errMu sync.Mutex
	runner.OnError(func(e ExtensionError) {
		errMu.Lock()
		recordedErrs = append(recordedErrs, e)
		errMu.Unlock()
	})

	// 1. Trigger re-entrant + cross-VM event bus delivery.
	busTool := runner.GetToolDefinition("trigger_bus")
	out, err := busTool.Execute(ctx, "t1", jsonx.NewObject(), nil, runner.CreateToolContext("t1", ctx))
	if err != nil {
		t.Fatalf("trigger_bus failed: %v", err)
	}
	gotA := out.Content[0].(*ai.TextContent).Text
	if !strings.Contains(gotA, "a:first") || !strings.Contains(gotA, "a:reentrant") {
		t.Fatalf("expected reentrant same-VM event bus delivery, got %q", gotA)
	}
	peerTool := runner.GetToolDefinition("read_peer_log")
	outB, err := peerTool.Execute(ctx, "t2", jsonx.NewObject(), nil, runner.CreateToolContext("t2", ctx))
	if err != nil {
		t.Fatalf("read_peer_log failed: %v", err)
	}
	gotB := outB.Content[0].(*ai.TextContent).Text
	if !strings.Contains(gotB, "b:first") || !strings.Contains(gotB, "b:reentrant") {
		t.Fatalf("expected cross-VM event bus delivery, got %q", gotB)
	}

	// 2. Sync throw isolated -> second extension still runs & blocks.
	tcRes := runner.EmitToolCall(ctx, ToolCallEvent{Type: "tool_call", ToolName: "any", Input: jsonx.NewObject()})
	if tcRes == nil || tcRes.Block == nil || !*tcRes.Block || tcRes.Reason == nil || *tcRes.Reason != "b-still-ran" {
		t.Fatalf("expected second extension to run after first threw, got %+v", tcRes)
	}

	// 3. Rejected async promise isolated & emitted to OnError.
	inRes := runner.EmitInput(ctx, "hello", nil, "interactive")
	if inRes.Action != "continue" {
		t.Fatalf("expected continue after isolated async reject, got %+v", inRes)
	}
	errMu.Lock()
	if len(recordedErrs) != 2 {
		t.Fatalf("expected 2 isolated ExtensionErrors (sync throw + async reject), got %d: %+v", len(recordedErrs), recordedErrs)
	}
	errMu.Unlock()
}

func TestQuickJSShutdownAndConcurrencyRace(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	timerTS := filepath.Join(dir, "timers.ts")
	if err := os.WriteFile(timerTS, []byte(`
export default function (pi: any) {
  let count = 0;
  setInterval(() => { count++; }, 5);
  pi.registerTool({
    name: "add_one",
    label: "Add One",
    description: "adds 1",
    parameters: { type: "object", properties: { n: { type: "number" } } },
    async execute(_id: string, params: any) {
      await new Promise((r) => setTimeout(r, 2));
      return { content: [{ type: "text", text: String((params.n || 0) + 1) }] };
    },
  });
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	res := LoadExtensions(ctx, []string{timerTS}, dir)
	if len(res.Errors) != 0 {
		t.Fatalf("LoadExtensions errors: %+v", res.Errors)
	}
	runner := NewExtensionRunner(res.Extensions, res.Runtime, dir, nil, nil)
	def := runner.GetToolDefinition("add_one")
	if def == nil {
		t.Fatalf("add_one not registered")
	}

	// Hammer the single VM concurrently across goroutines.
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			out, err := def.Execute(ctx, "c", teqjsJSONObject(map[string]any{"n": idx}), nil, runner.CreateToolContext("c", ctx))
			if err != nil {
				t.Errorf("concurrent execute failed: %v", err)
				return
			}
			if len(out.Content) == 0 {
				t.Errorf("empty content from add_one")
			}
		}(i)
	}
	wg.Wait()

	// Shutdown with active setInterval must finish cleanly without hang or race.
	done := make(chan struct{}, 1)
	go func() {
		runner.Shutdown()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("runner.Shutdown hung with active setInterval")
	}
}
