// Ported from packages/coding-agent/src/package-manager-cli.ts (pi v1.5.2).

package codingagent

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/cli"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/tui"
)

type tpmcFakeTerminal struct {
	mu      sync.Mutex
	onInput func(string)
	writes  []string
	writeCh chan struct{}
}

func tpmcNewFakeTerminal() *tpmcFakeTerminal {
	return &tpmcFakeTerminal{writeCh: make(chan struct{}, 32)}
}

func (t *tpmcFakeTerminal) Start(onInput func(data string), _ func()) {
	t.mu.Lock()
	t.onInput = onInput
	t.mu.Unlock()
}

func (t *tpmcFakeTerminal) Stop()                              {}
func (t *tpmcFakeTerminal) DrainInput(_, _ *int64) error       { return nil }
func (t *tpmcFakeTerminal) Columns() int                       { return 100 }
func (t *tpmcFakeTerminal) Rows() int                          { return 30 }
func (t *tpmcFakeTerminal) KittyProtocolActive() bool          { return false }
func (t *tpmcFakeTerminal) MoveBy(int)                         {}
func (t *tpmcFakeTerminal) HideCursor()                        {}
func (t *tpmcFakeTerminal) ShowCursor()                        {}
func (t *tpmcFakeTerminal) ClearLine()                         {}
func (t *tpmcFakeTerminal) ClearFromCursor()                   {}
func (t *tpmcFakeTerminal) ClearScreen()                       {}
func (t *tpmcFakeTerminal) SetTitle(string)                    {}
func (t *tpmcFakeTerminal) SetProgress(bool)                   {}
func (t *tpmcFakeTerminal) SetProgramStatus(tui.ProgramStatus) {}

func (t *tpmcFakeTerminal) Write(data string) {
	t.mu.Lock()
	t.writes = append(t.writes, data)
	t.mu.Unlock()
	select {
	case t.writeCh <- struct{}{}:
	default:
	}
}

func (t *tpmcFakeTerminal) waitReadyAndSend(tb testing.TB, key string) {
	tb.Helper()
	deadline := time.After(3 * time.Second)
	for {
		t.mu.Lock()
		fn := t.onInput
		ready := fn != nil && len(t.writes) > 0
		t.mu.Unlock()
		if ready {
			fn(key)
			return
		}
		select {
		case <-t.writeCh:
		case <-deadline:
			tb.Fatal("timed out waiting for config TUI render")
		}
	}
}

func TestHandleConfigCommandHelpValidationLocalTrustAndTui(t *testing.T) {
	t.Cleanup(func() { _, _ = theme.InitTheme("dark") })
	_, _ = theme.InitTheme("dark")

	handled, code := HandleConfigCommand(context.Background(), []string{"--help"})
	if handled || code != 0 {
		t.Fatalf("non-config arg handled=(%v, %d), want (false, 0)", handled, code)
	}

	var stdout, stderr bytes.Buffer
	handled, code = HandleConfigCommand(context.Background(), []string{"config", "--help"}, &PackageCommandOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if !handled || code != 0 || !strings.Contains(stdout.String(), "config [-l]") {
		t.Fatalf("config --help = (%v, %d) out=%q", handled, code, stdout.String())
	}

	// Unknown flag returns exit code 1.
	stderr.Reset()
	handled, code = HandleConfigCommand(context.Background(), []string{"config", "--bogus"}, &PackageCommandOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if !handled || code != 1 || !strings.Contains(stderr.String(), "Unknown option --bogus") {
		t.Fatalf("config --bogus = (%v, %d) err=%q", handled, code, stderr.String())
	}

	// Local config without trust (--no-approve) fails cleanly when project resources exist.
	cwd := t.TempDir()
	agentDir := filepath.Join(t.TempDir(), "agent")
	if err := os.MkdirAll(filepath.Join(cwd, ".pi"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".pi", "settings.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	stderr.Reset()
	handled, code = HandleConfigCommand(context.Background(), []string{"config", "-l", "--no-approve"}, &PackageCommandOptions{
		Cwd:      cwd,
		AgentDir: agentDir,
		Stdout:   &stdout,
		Stderr:   &stderr,
	})
	if !handled || code != 1 || !strings.Contains(stderr.String(), "Project is not trusted") {
		t.Fatalf("config -l --no-approve = (%v, %d) err=%q", handled, code, stderr.String())
	}

	// Config selector opens and exits cleanly via Escape.
	term := tpmcNewFakeTerminal()
	type res struct {
		handled bool
		code    int
	}
	ch := make(chan res, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		h, c := HandleConfigCommand(ctx, []string{"config", "--approve"}, &PackageCommandOptions{
			Cwd:      cwd,
			AgentDir: agentDir,
			Stdout:   &stdout,
			Stderr:   &stderr,
			ConfigUiOptions: &cli.ConfigSelectorOptions{
				Terminal:         term,
				DisableThemeInit: true,
			},
		})
		ch <- res{handled: h, code: c}
	}()

	term.waitReadyAndSend(t, "\x1b")
	got := <-ch
	if !got.handled || got.code != 0 {
		t.Fatalf("config interactive TUI = (%v, %d), want (true, 0)", got.handled, got.code)
	}
}

func TestHandlePackageCommandBareboneUnsupportedAndUpdateModels(t *testing.T) {
	if handled, code := HandlePackageCommand(context.Background(), []string{"--version"}); handled || code != 0 {
		t.Fatalf("non-package cmd = (%v, %d), want (false, 0)", handled, code)
	}

	var stdout, stderr bytes.Buffer
	handled, code := HandlePackageCommand(context.Background(), []string{"install", "--help"}, &PackageCommandOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if !handled || code != 0 || !strings.Contains(stdout.String(), "Install a package") {
		t.Fatalf("install --help = (%v, %d) out=%q", handled, code, stdout.String())
	}

	// Missing source returns usage error.
	stderr.Reset()
	handled, code = HandlePackageCommand(context.Background(), []string{"install"}, &PackageCommandOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if !handled || code != 1 || !strings.Contains(stderr.String(), "Missing install source.") {
		t.Fatalf("install missing source = (%v, %d) err=%q", handled, code, stderr.String())
	}

	// External npm/git commands return clear unsupported error & exit 1.
	for _, argv := range [][]string{
		{"install", "npm:foo"},
		{"remove", "npm:foo"},
		{"uninstall", "npm:foo"},
		{"list"},
		{"update"},
	} {
		stderr.Reset()
		handled, code = HandlePackageCommand(context.Background(), argv, &PackageCommandOptions{
			Stdout: &stdout,
			Stderr: &stderr,
		})
		if !handled || code != 1 || !strings.Contains(stderr.String(), "barebone") {
			t.Fatalf("%v = (%v, %d) err=%q, want barebone unsupported", argv, handled, code, stderr.String())
		}
	}

	// Conflicting update flags fail cleanly.
	stderr.Reset()
	handled, code = HandlePackageCommand(context.Background(), []string{"update", "--models", "--all"}, &PackageCommandOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if !handled || code != 1 || !strings.Contains(stderr.String(), "cannot be combined") {
		t.Fatalf("update conflict = (%v, %d) err=%q", handled, code, stderr.String())
	}

	// `update --models` runs ModelRuntime.Refresh cleanly with empty models/auth.
	t.Setenv("PI_OFFLINE", "1")
	for _, envKey := range []string{
		"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_OAUTH_TOKEN", "ANTHROPIC_API_KEY",
		"OPENAI_API_KEY", "AZURE_OPENAI_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY",
		"GOOGLE_CLOUD_API_KEY", "GOOGLE_APPLICATION_CREDENTIALS", "GOOGLE_CLOUD_PROJECT",
		"GCLOUD_PROJECT", "GOOGLE_CLOUD_LOCATION",
		"AWS_PROFILE", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_BEARER_TOKEN_BEDROCK",
		"AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "AWS_CONTAINER_CREDENTIALS_FULL_URI",
		"AWS_WEB_IDENTITY_TOKEN_FILE",
		"OPENROUTER_API_KEY", "AI_GATEWAY_API_KEY", "COPILOT_GITHUB_TOKEN", "GITHUB_TOKEN", "GH_TOKEN",
		"GROQ_API_KEY", "CEREBRAS_API_KEY", "XAI_API_KEY", "MISTRAL_API_KEY", "DEEPSEEK_API_KEY",
		"NVIDIA_API_KEY", "HF_TOKEN", "FIREWORKS_API_KEY", "TOGETHER_API_KEY", "BASETEN_API_KEY",
		"OPENCODE_API_KEY", "KIMI_API_KEY", "META_API_KEY", "CLOUDFLARE_API_KEY",
		"MINIMAX_API_KEY", "MINIMAX_CN_API_KEY", "MOONSHOT_API_KEY", "ZAI_API_KEY", "ZAI_CODING_CN_API_KEY",
		"TYPESAFE_API_KEY", "RADIUS_API_KEY", "RADIUS_GATEWAY", "ANT_LING_API_KEY",
		"QWEN_TOKEN_PLAN_TEAM_API_KEY", "QWEN_TOKEN_PLAN_API_KEY",
		"QWEN_TOKEN_PLAN_TEAM_CN_API_KEY", "QWEN_TOKEN_PLAN_CN_API_KEY",
		"QWEN_TOKEN_PLAN_INDIVIDUAL_API_KEY", "QWEN_TOKEN_PLAN_INDIVIDUAL_CN_API_KEY",
		"XIAOMI_API_KEY", "XIAOMI_TOKEN_PLAN_CN_API_KEY", "XIAOMI_TOKEN_PLAN_AMS_API_KEY", "XIAOMI_TOKEN_PLAN_SGP_API_KEY",
	} {
		t.Setenv(envKey, "")
	}
	stdout.Reset()
	stderr.Reset()
	agentDir := t.TempDir()
	authPath := filepath.Join(agentDir, "auth.json")
	modelsPath := filepath.Join(agentDir, "models.json")
	rt, err := core.CreateModelRuntime(context.Background(), &core.CreateModelRuntimeOptions{
		Credentials: ai.NewInMemoryCredentialStore(),
		AuthPath:    &authPath,
		ModelsPath:  &modelsPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	handled, code = HandlePackageCommand(context.Background(), []string{"update", "--models"}, &PackageCommandOptions{
		AgentDir:     agentDir,
		Stdout:       &stdout,
		Stderr:       &stderr,
		ModelRuntime: rt,
	})
	if !handled || code != 0 || !strings.Contains(stdout.String(), "Model catalogs refreshed") {
		t.Fatalf("update --models = (%v, %d) out=%q err=%q", handled, code, stdout.String(), stderr.String())
	}
}
