// Ported from packages/coding-agent/src/main.ts (pi v1.5.2) CLI dispatch & integration contracts.

package codingagent_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/codingagent"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes"
	"github.com/keejkrej/pi-go/codingagent/modes/gui"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive"
	"github.com/keejkrej/pi-go/codingagent/modes/rpc"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func tmBoolPtr(v bool) *bool    { return &v }
func tmStrPtr(v string) *string { return &v }

type tmTestHarness struct {
	cwd        string
	agentDir   string
	authPath   string
	modelsPath string
	sessionDir string
}

func tmNewHarness(t *testing.T) tmTestHarness {
	t.Helper()
	t.Setenv("PI_OFFLINE", "1")
	t.Setenv("PI_SKIP_VERSION_CHECK", "1")
	for _, envKey := range []string{
		"ANTHROPIC_API_KEY", "ANTHROPIC_OAUTH_TOKEN", "ANTHROPIC_AUTH_TOKEN",
		"OPENAI_API_KEY", "AZURE_OPENAI_API_KEY", "OPENROUTER_API_KEY", "AI_GATEWAY_API_KEY",
		"GOOGLE_API_KEY", "GEMINI_API_KEY", "GOOGLE_CLOUD_API_KEY",
		"GOOGLE_APPLICATION_CREDENTIALS", "GOOGLE_CLOUD_PROJECT", "GCLOUD_PROJECT", "GOOGLE_CLOUD_LOCATION",
		"AWS_PROFILE", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_BEARER_TOKEN_BEDROCK",
		"AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_WEB_IDENTITY_TOKEN_FILE",
		"COPILOT_GITHUB_TOKEN", "GITHUB_TOKEN", "GH_TOKEN",
		"XAI_API_KEY", "GROQ_API_KEY", "MISTRAL_API_KEY", "CEREBRAS_API_KEY", "DEEPSEEK_API_KEY",
		"NVIDIA_API_KEY", "HF_TOKEN", "FIREWORKS_API_KEY", "TOGETHER_API_KEY", "BASETEN_API_KEY",
		"OPENCODE_API_KEY", "KIMI_API_KEY", "META_API_KEY", "CLOUDFLARE_API_KEY",
		"MINIMAX_API_KEY", "MINIMAX_CN_API_KEY", "MOONSHOT_API_KEY", "ZAI_API_KEY", "ZAI_CODING_CN_API_KEY",
		"TYPESAFE_API_KEY", "RADIUS_API_KEY", "RADIUS_GATEWAY", "ANT_LING_API_KEY",
		"QWEN_TOKEN_PLAN_TEAM_API_KEY", "QWEN_TOKEN_PLAN_API_KEY",
		"QWEN_TOKEN_PLAN_TEAM_CN_API_KEY", "QWEN_TOKEN_PLAN_CN_API_KEY",
		"QWEN_TOKEN_PLAN_INDIVIDUAL_API_KEY", "QWEN_TOKEN_PLAN_INDIVIDUAL_CN_API_KEY",
		"XIAOMI_API_KEY", "XIAOMI_TOKEN_PLAN_CN_API_KEY", "XIAOMI_TOKEN_PLAN_AMS_API_KEY", "XIAOMI_TOKEN_PLAN_SGP_API_KEY",
		"PI_STARTUP_BENCHMARK",
	} {
		t.Setenv(envKey, "")
	}

	root := t.TempDir()
	cwd := filepath.Join(root, "workspace")
	agentDir := filepath.Join(root, "agent")
	sessionDir := filepath.Join(root, "sessions")
	for _, dir := range []string{cwd, agentDir, sessionDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	t.Setenv(utils.EnvAgentDir, agentDir)
	t.Setenv(utils.EnvSessionDir, sessionDir)

	authPath := filepath.Join(agentDir, "auth.json")
	modelsPath := filepath.Join(agentDir, "models.json")
	return tmTestHarness{
		cwd:        cwd,
		agentDir:   agentDir,
		authPath:   authPath,
		modelsPath: modelsPath,
		sessionDir: sessionDir,
	}
}

func tmSeedAuth(t *testing.T, h tmTestHarness, provider, apiKey string) {
	t.Helper()
	doc := map[string]map[string]string{
		provider: {
			"type": "api_key",
			"key":  apiKey,
		},
	}
	encoded, err := jsonx.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal auth.json: %v", err)
	}
	if err := os.WriteFile(h.authPath, encoded, 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}
}

func tmBaseMainOpts(h tmTestHarness, stdout, stderr *bytes.Buffer) *codingagent.MainOptions {
	return &codingagent.MainOptions{
		Stdin:       strings.NewReader(""),
		Stdout:      stdout,
		Stderr:      stderr,
		StdinIsTTY:  tmBoolPtr(false),
		StdoutIsTTY: tmBoolPtr(false),
		Cwd:         h.cwd,
		AgentDir:    h.agentDir,
		AuthPath:    &h.authPath,
		ModelsPath:  &h.modelsPath,
		SessionDir:  &h.sessionDir,
		RunInteractiveMode: func(_ context.Context, _ *core.AgentSessionRuntime, _ *interactive.InteractiveModeOptions) error {
			return nil
		},
		RunGuiMode: func(_ context.Context, _ *core.AgentSessionRuntime, _ *gui.GuiModeOptions) error {
			return nil
		},
		RunPrintMode: func(_ context.Context, _ *core.AgentSessionRuntime, _ *modes.PrintModeOptions) int {
			return 0
		},
		RunRpcMode: func(_ context.Context, _ *core.AgentSessionRuntime, _ *rpc.RpcModeOptions) error {
			return nil
		},
	}
}

func TestMainVersionAndHelp(t *testing.T) {
	h := tmNewHarness(t)

	for _, defaultGui := range []bool{false, true} {
		var stdout, stderr bytes.Buffer
		opts := tmBaseMainOpts(h, &stdout, &stderr)
		opts.DefaultGuiMode = defaultGui
		calledMode := false
		opts.RunGuiMode = func(_ context.Context, _ *core.AgentSessionRuntime, _ *gui.GuiModeOptions) error {
			calledMode = true
			return nil
		}
		opts.RunInteractiveMode = func(_ context.Context, _ *core.AgentSessionRuntime, _ *interactive.InteractiveModeOptions) error {
			calledMode = true
			return nil
		}

		code := codingagent.Main(t.Context(), []string{"--version"}, opts)
		if code != 0 {
			t.Fatalf("defaultGui=%v --version code=%d stderr=%s", defaultGui, code, stderr.String())
		}
		if strings.TrimSpace(stdout.String()) != utils.VERSION {
			t.Fatalf("defaultGui=%v --version got %q want %q", defaultGui, stdout.String(), utils.VERSION)
		}
		if calledMode {
			t.Fatalf("defaultGui=%v --version should not start UI mode", defaultGui)
		}

		stdout.Reset()
		stderr.Reset()
		code = codingagent.Main(t.Context(), []string{"--help"}, opts)
		if code != 0 {
			t.Fatalf("defaultGui=%v --help code=%d stderr=%s", defaultGui, code, stderr.String())
		}
		helpText := stdout.String()
		for _, want := range []string{"--gui", "--mode <mode>", "--offline"} {
			if !strings.Contains(helpText, want) {
				t.Fatalf("defaultGui=%v --help missing %q:\n%s", defaultGui, want, helpText)
			}
		}
		if calledMode {
			t.Fatalf("defaultGui=%v --help should not start UI mode", defaultGui)
		}
	}
}

func TestMainListModelsAndAuthCommands(t *testing.T) {
	h := tmNewHarness(t)
	tmSeedAuth(t, h, "anthropic", "sk-test")

	t.Run("list-models exits without gui", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		opts := tmBaseMainOpts(h, &stdout, &stderr)
		opts.DefaultGuiMode = true
		guiCalled := false
		opts.RunGuiMode = func(_ context.Context, _ *core.AgentSessionRuntime, _ *gui.GuiModeOptions) error {
			guiCalled = true
			return nil
		}

		code := codingagent.Main(t.Context(), []string{"--list-models", "claude"}, opts)
		if code != 0 {
			t.Fatalf("--list-models code=%d stderr=%s", code, stderr.String())
		}
		if guiCalled {
			t.Fatal("--list-models must exit before starting GUI mode")
		}
		if !strings.Contains(stdout.String(), "claude") {
			t.Fatalf("expected claude models in output, got:\n%s", stdout.String())
		}
	})

	t.Run("auth help and check", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		opts := tmBaseMainOpts(h, &stdout, &stderr)
		opts.DefaultGuiMode = true

		code := codingagent.Main(t.Context(), []string{"auth", "--help"}, opts)
		if code != 0 || !strings.Contains(stdout.String(), "print-api-key") {
			t.Fatalf("auth --help code=%d out=%s err=%s", code, stdout.String(), stderr.String())
		}

		stdout.Reset()
		stderr.Reset()
		code = codingagent.Main(t.Context(), []string{"auth", "check", "--provider", "anthropic", "--json", "--no-refresh"}, opts)
		if code != 0 {
			t.Fatalf("auth check code=%d out=%s err=%s", code, stdout.String(), stderr.String())
		}
		var payload map[string]any
		if err := jsonx.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &payload); err != nil {
			t.Fatalf("invalid json output %q: %v", stdout.String(), err)
		}
		if payload["status"] != "ready" || payload["provider"] != "anthropic" {
			t.Fatalf("unexpected auth check payload: %+v", payload)
		}

		stdout.Reset()
		stderr.Reset()
		code = codingagent.Main(t.Context(), []string{"auth", "check", "--provider", "openai", "--json", "--no-refresh"}, opts)
		if code != 1 {
			t.Fatalf("unconfigured provider code=%d want 1 out=%s err=%s", code, stdout.String(), stderr.String())
		}

		stdout.Reset()
		stderr.Reset()
		code = codingagent.Main(t.Context(), []string{"auth", "check", "--bogus"}, opts)
		if code != 1 || !strings.Contains(stderr.String(), "Unknown option") {
			t.Fatalf("unknown auth option code=%d stderr=%s", code, stderr.String())
		}
	})

	t.Run("auth print-api-key", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		opts := tmBaseMainOpts(h, &stdout, &stderr)
		code := codingagent.Main(t.Context(), []string{"auth", "print-api-key", "--provider", "anthropic"}, opts)
		if code != 0 || strings.TrimSpace(stdout.String()) != "sk-test" {
			t.Fatalf("auth print-api-key code=%d out=%q err=%s", code, stdout.String(), stderr.String())
		}
	})
}

func TestMainModeRoutingAndDefaultGuiMode(t *testing.T) {
	h := tmNewHarness(t)
	tmSeedAuth(t, h, "anthropic", "sk-test")

	t.Run("DefaultGuiMode empty non-TTY stdin stays GUI", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		opts := tmBaseMainOpts(h, &stdout, &stderr)
		opts.DefaultGuiMode = true
		opts.Stdin = strings.NewReader("   \n ")
		opts.StdinIsTTY = tmBoolPtr(false)
		opts.StdoutIsTTY = tmBoolPtr(false)

		called := ""
		var gotOpts *gui.GuiModeOptions
		opts.RunGuiMode = func(_ context.Context, _ *core.AgentSessionRuntime, gopts *gui.GuiModeOptions) error {
			called = "gui"
			gotOpts = gopts
			return nil
		}
		opts.RunPrintMode = func(_ context.Context, _ *core.AgentSessionRuntime, _ *modes.PrintModeOptions) int {
			called = "print"
			return 0
		}

		code := codingagent.Main(t.Context(), []string{"--verbose", "hello"}, opts)
		if code != 0 || called != "gui" {
			t.Fatalf("got code=%d called=%q want gui, stderr=%s", code, called, stderr.String())
		}
		if gotOpts == nil || !gotOpts.Verbose || gotOpts.InitialMessage == nil || *gotOpts.InitialMessage != "hello" {
			t.Fatalf("unexpected GuiModeOptions: %+v", gotOpts)
		}
	})

	t.Run("DefaultGuiMode non-empty piped stdin switches to print mode", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		opts := tmBaseMainOpts(h, &stdout, &stderr)
		opts.DefaultGuiMode = true
		opts.Stdin = strings.NewReader("piped prompt\n")
		opts.StdinIsTTY = tmBoolPtr(false)
		opts.StdoutIsTTY = tmBoolPtr(false)

		called := ""
		var gotPrint *modes.PrintModeOptions
		opts.RunGuiMode = func(_ context.Context, _ *core.AgentSessionRuntime, _ *gui.GuiModeOptions) error {
			called = "gui"
			return nil
		}
		opts.RunPrintMode = func(_ context.Context, _ *core.AgentSessionRuntime, popts *modes.PrintModeOptions) int {
			called = "print"
			gotPrint = popts
			return 0
		}

		code := codingagent.Main(t.Context(), []string{"--provider", "anthropic", "--model", "claude"}, opts)
		if code != 0 || called != "print" {
			t.Fatalf("got code=%d called=%q want print, stderr=%s", code, called, stderr.String())
		}
		if gotPrint == nil || gotPrint.Mode != modes.PrintModeOutputText || gotPrint.InitialMessage == nil || *gotPrint.InitialMessage != "piped prompt" {
			t.Fatalf("unexpected PrintModeOptions: %+v", gotPrint)
		}
	})

	t.Run("explicit --gui keeps non-empty piped stdin in GUI mode", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		opts := tmBaseMainOpts(h, &stdout, &stderr)
		opts.DefaultGuiMode = true
		opts.Stdin = strings.NewReader("piped for gui\n")
		opts.StdinIsTTY = tmBoolPtr(false)

		called := ""
		var gotGui *gui.GuiModeOptions
		opts.RunGuiMode = func(_ context.Context, _ *core.AgentSessionRuntime, gopts *gui.GuiModeOptions) error {
			called = "gui"
			gotGui = gopts
			return nil
		}
		opts.RunPrintMode = func(_ context.Context, _ *core.AgentSessionRuntime, _ *modes.PrintModeOptions) int {
			called = "print"
			return 0
		}

		code := codingagent.Main(t.Context(), []string{"--gui"}, opts)
		if code != 0 || called != "gui" {
			t.Fatalf("got code=%d called=%q want gui, stderr=%s", code, called, stderr.String())
		}
		if gotGui == nil || gotGui.InitialMessage == nil || *gotGui.InitialMessage != "piped for gui" {
			t.Fatalf("expected piped stdin in InitialMessage, got %+v", gotGui)
		}
	})

	t.Run("explicit overrides on DefaultGuiMode: -p, --mode json, --mode text, --mode rpc", func(t *testing.T) {
		cases := []struct {
			name     string
			args     []string
			wantMode string
			wantOut  modes.PrintModeOutput
		}{
			{name: "print flag", args: []string{"-p", "--provider", "anthropic", "--model", "claude"}, wantMode: "print", wantOut: modes.PrintModeOutputText},
			{name: "mode json", args: []string{"--mode", "json", "--provider", "anthropic", "--model", "claude"}, wantMode: "print", wantOut: modes.PrintModeOutputJson},
			{name: "mode text", args: []string{"--mode", "text", "--provider", "anthropic", "--model", "claude"}, wantMode: "print", wantOut: modes.PrintModeOutputText},
			{name: "mode rpc", args: []string{"--mode", "rpc", "--provider", "anthropic", "--model", "claude"}, wantMode: "rpc"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				var stdout, stderr bytes.Buffer
				opts := tmBaseMainOpts(h, &stdout, &stderr)
				opts.DefaultGuiMode = true
				opts.StdinIsTTY = tmBoolPtr(true)
				opts.StdoutIsTTY = tmBoolPtr(true)

				called := ""
				var gotOut modes.PrintModeOutput
				opts.RunGuiMode = func(_ context.Context, _ *core.AgentSessionRuntime, _ *gui.GuiModeOptions) error {
					called = "gui"
					return nil
				}
				opts.RunInteractiveMode = func(_ context.Context, _ *core.AgentSessionRuntime, _ *interactive.InteractiveModeOptions) error {
					called = "interactive"
					return nil
				}
				opts.RunPrintMode = func(_ context.Context, _ *core.AgentSessionRuntime, popts *modes.PrintModeOptions) int {
					called = "print"
					gotOut = popts.Mode
					return 0
				}
				opts.RunRpcMode = func(_ context.Context, _ *core.AgentSessionRuntime, _ *rpc.RpcModeOptions) error {
					called = "rpc"
					return nil
				}

				code := codingagent.Main(t.Context(), tc.args, opts)
				if code != 0 || called != tc.wantMode {
					t.Fatalf("case %s: code=%d called=%q want=%q stderr=%s", tc.name, code, called, tc.wantMode, stderr.String())
				}
				if tc.wantMode == "print" && gotOut != tc.wantOut {
					t.Fatalf("case %s: print output=%q want=%q", tc.name, gotOut, tc.wantOut)
				}
			})
		}
	})

	t.Run("CLI default TTY routes to interactive and --mode gui routes to gui", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		opts := tmBaseMainOpts(h, &stdout, &stderr)
		opts.DefaultGuiMode = false
		opts.StdinIsTTY = tmBoolPtr(true)
		opts.StdoutIsTTY = tmBoolPtr(true)

		called := ""
		opts.RunInteractiveMode = func(_ context.Context, _ *core.AgentSessionRuntime, _ *interactive.InteractiveModeOptions) error {
			called = "interactive"
			return nil
		}
		opts.RunGuiMode = func(_ context.Context, _ *core.AgentSessionRuntime, _ *gui.GuiModeOptions) error {
			called = "gui"
			return nil
		}

		if code := codingagent.Main(t.Context(), nil, opts); code != 0 || called != "interactive" {
			t.Fatalf("default CLI TTY: code=%d called=%q want interactive stderr=%s", code, called, stderr.String())
		}

		called = ""
		if code := codingagent.Main(t.Context(), []string{"--mode", "gui"}, opts); code != 0 || called != "gui" {
			t.Fatalf("--mode gui: code=%d called=%q want gui stderr=%s", code, called, stderr.String())
		}
	})
}

func TestMainGuiAllowsNoModelWhilePrintFailsCleanly(t *testing.T) {
	h := tmNewHarness(t) // intentionally NO auth seeded

	var stdout, stderr bytes.Buffer
	opts := tmBaseMainOpts(h, &stdout, &stderr)
	opts.DefaultGuiMode = true
	guiCalled := false
	opts.RunGuiMode = func(_ context.Context, rt *core.AgentSessionRuntime, _ *gui.GuiModeOptions) error {
		guiCalled = true
		if m := rt.Session.Model(); m != nil {
			t.Fatalf("expected nil session model without configured auth, got %s/%s", m.Provider, m.Id)
		}
		return nil
	}
	if code := codingagent.Main(t.Context(), nil, opts); code != 0 || !guiCalled {
		t.Fatalf("expected GUI mode to launch with no model, code=%d guiCalled=%v stderr=%s", code, guiCalled, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	printCalled := false
	opts.RunPrintMode = func(_ context.Context, _ *core.AgentSessionRuntime, _ *modes.PrintModeOptions) int {
		printCalled = true
		return 0
	}
	code := codingagent.Main(t.Context(), []string{"-p", "hi"}, opts)
	if code != 1 || printCalled || !strings.Contains(stderr.String(), "No models available") {
		t.Fatalf("expected print mode with no model to fail, code=%d printCalled=%v stderr=%s", code, printCalled, stderr.String())
	}
}

func TestMainValidationAndSubcommands(t *testing.T) {
	h := tmNewHarness(t)
	tmSeedAuth(t, h, "anthropic", "sk-test")

	t.Run("package commands return unsupported in barebone while update --help succeeds", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		opts := tmBaseMainOpts(h, &stdout, &stderr)
		opts.DefaultGuiMode = true
		code := codingagent.Main(t.Context(), []string{"install", "npm:foo"}, opts)
		if code != 1 {
			t.Fatalf("install expected exit 1, got %d stderr=%s", code, stderr.String())
		}

		stdout.Reset()
		stderr.Reset()
		code = codingagent.Main(t.Context(), []string{"update", "--help"}, opts)
		if code != 0 {
			t.Fatalf("update --help expected exit 0, got %d stderr=%s", code, stderr.String())
		}
	})

	t.Run("rpc with @file fails cleanly", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		opts := tmBaseMainOpts(h, &stdout, &stderr)
		code := codingagent.Main(t.Context(), []string{"--mode", "rpc", "@notes.txt"}, opts)
		if code != 1 || !strings.Contains(stderr.String(), "@file arguments are not supported in RPC mode") {
			t.Fatalf("code=%d stderr=%s", code, stderr.String())
		}
	})

	t.Run("empty --name and conflicting --fork fail cleanly", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		opts := tmBaseMainOpts(h, &stdout, &stderr)
		code := codingagent.Main(t.Context(), []string{"--name", "   "}, opts)
		if code != 1 || !strings.Contains(stderr.String(), "--name requires a non-empty value") {
			t.Fatalf("--name code=%d stderr=%s", code, stderr.String())
		}

		stdout.Reset()
		stderr.Reset()
		code = codingagent.Main(t.Context(), []string{"--fork", "abc", "--continue"}, opts)
		if code != 1 || !strings.Contains(stderr.String(), "--fork cannot be combined") {
			t.Fatalf("--fork conflict code=%d stderr=%s", code, stderr.String())
		}
	})

	t.Run("unknown flag forwarded via --api-key validation and export error", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		opts := tmBaseMainOpts(h, &stdout, &stderr)
		code := codingagent.Main(t.Context(), []string{"--api-key", "sk-inline", "-p", "hi"}, opts)
		if code != 1 || !strings.Contains(stderr.String(), "--api-key requires a model") {
			t.Fatalf("--api-key code=%d stderr=%s", code, stderr.String())
		}

		stdout.Reset()
		stderr.Reset()
		code = codingagent.Main(t.Context(), []string{"--export", filepath.Join(h.cwd, "does-not-exist.jsonl")}, opts)
		if code != 1 {
			t.Fatalf("--export missing expected exit 1, got %d", code)
		}
	})

	t.Run("RpcEntry prepends --mode rpc", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		opts := tmBaseMainOpts(h, &stdout, &stderr)
		rpcCalled := false
		opts.RunRpcMode = func(_ context.Context, _ *core.AgentSessionRuntime, _ *rpc.RpcModeOptions) error {
			rpcCalled = true
			return nil
		}
		code := codingagent.RpcEntry(t.Context(), []string{"--provider", "anthropic", "--model", "claude"}, opts)
		if code != 0 || !rpcCalled {
			t.Fatalf("RpcEntry code=%d rpcCalled=%v stderr=%s", code, rpcCalled, stderr.String())
		}
	})

	t.Run("no mcp subcommand wired in barebone", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		opts := tmBaseMainOpts(h, &stdout, &stderr)
		code := codingagent.Main(t.Context(), []string{"mcp", "unknown", "--version"}, opts)
		if code != 0 || strings.TrimSpace(stdout.String()) != utils.VERSION {
			t.Fatalf("expected mcp arg to fall through as positional without mcp handler, code=%d out=%q err=%s", code, stdout.String(), stderr.String())
		}
	})

	t.Run("deleted cwd exits cleanly when os.Getwd fails", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			return
		}
		origWd, err := os.Getwd()
		if err != nil {
			t.Fatalf("Getwd: %v", err)
		}
		t.Cleanup(func() {
			_ = os.Chdir(origWd)
		})
		doomed := t.TempDir()
		if err := os.Chdir(doomed); err != nil {
			t.Fatalf("Chdir(%s): %v", doomed, err)
		}
		if err := os.RemoveAll(doomed); err != nil {
			t.Fatalf("RemoveAll(%s): %v", doomed, err)
		}
		if _, wdErr := os.Getwd(); wdErr == nil {
			// Platform preserves cwd inode path after unlink.
			return
		}
		var stdout, stderr bytes.Buffer
		opts := tmBaseMainOpts(h, &stdout, &stderr)
		opts.Cwd = ""
		code := codingagent.Main(t.Context(), []string{"--version"}, opts)
		if code != 1 {
			t.Fatalf("expected exit 1 on deleted cwd, got %d", code)
		}
	})
}
