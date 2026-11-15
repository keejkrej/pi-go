// Ported from packages/coding-agent/src/package-manager-cli.ts (pi v1.5.2).

package codingagent

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
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
	"github.com/keejkrej/pi-go/internal/jsonx"
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

func tpmcBuildTgz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for name, content := range files {
		hdr := &tar.Header{
			Name:     name,
			Typeflag: tar.TypeReg,
			Mode:     0o644,
			Size:     int64(len(content)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestHandlePackageCommandLifecycleAndUpdateModels(t *testing.T) {
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

	// Unknown option and conflicting update flags fail cleanly.
	stderr.Reset()
	handled, code = HandlePackageCommand(context.Background(), []string{"update", "--models", "--all"}, &PackageCommandOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if !handled || code != 1 || !strings.Contains(stderr.String(), "cannot be combined") {
		t.Fatalf("update conflict = (%v, %d) err=%q", handled, code, stderr.String())
	}

	// Offline mock npm server for end-to-end CLI install / list / update / remove / uninstall.
	var verMu sync.Mutex
	latestVer := "1.0.0"
	tarV1 := tpmcBuildTgz(t, map[string]string{
		"package/package.json":       `{"name":"cli-demo","version":"1.0.0"}`,
		"package/extensions/main.ts": "export default function() {}",
	})
	tarV2 := tpmcBuildTgz(t, map[string]string{
		"package/package.json":       `{"name":"cli-demo","version":"1.2.0"}`,
		"package/extensions/main.ts": "export default function() {} // v2",
	})
	scopedTar := tpmcBuildTgz(t, map[string]string{
		"package/package.json":    `{"name":"@cli/local-pkg","version":"1.0.0"}`,
		"package/prompts/help.md": "# help",
	})

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cli-demo":
			verMu.Lock()
			v := latestVer
			verMu.Unlock()
			versions := map[string]any{
				"1.0.0": map[string]any{
					"name":    "cli-demo",
					"version": "1.0.0",
					"dist":    map[string]any{"tarball": srv.URL + "/tgz/cli-demo-1.0.0.tgz"},
				},
			}
			if v == "1.2.0" {
				versions["1.2.0"] = map[string]any{
					"name":    "cli-demo",
					"version": "1.2.0",
					"dist":    map[string]any{"tarball": srv.URL + "/tgz/cli-demo-1.2.0.tgz"},
				}
			}
			if payload, err := jsonx.Marshal(map[string]any{
				"name":      "cli-demo",
				"dist-tags": map[string]any{"latest": v},
				"versions":  versions,
			}); err == nil {
				_, _ = w.Write(payload)
			}
		case "/@cli/local-pkg":
			if payload, err := jsonx.Marshal(map[string]any{
				"name":      "@cli/local-pkg",
				"dist-tags": map[string]any{"latest": "1.0.0"},
				"versions": map[string]any{
					"1.0.0": map[string]any{
						"name":    "@cli/local-pkg",
						"version": "1.0.0",
						"dist":    map[string]any{"tarball": srv.URL + "/tgz/scoped-1.0.0.tgz"},
					},
				},
			}); err == nil {
				_, _ = w.Write(payload)
			}
		case "/tgz/cli-demo-1.0.0.tgz":
			_, _ = w.Write(tarV1)
		case "/tgz/cli-demo-1.2.0.tgz":
			_, _ = w.Write(tarV2)
		case "/tgz/scoped-1.0.0.tgz":
			_, _ = w.Write(scopedTar)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("NPM_CONFIG_REGISTRY", srv.URL)

	cwd := t.TempDir()
	agentDir := filepath.Join(t.TempDir(), "agent")
	cliOpts := func() *PackageCommandOptions {
		falseBool := false
		return &PackageCommandOptions{
			Cwd:        cwd,
			AgentDir:   agentDir,
			Stdout:     &stdout,
			Stderr:     &stderr,
			AllowUi:    &falseBool,
			HTTPClient: srv.Client(),
		}
	}

	// Empty list initially.
	stdout.Reset()
	stderr.Reset()
	handled, code = HandlePackageCommand(context.Background(), []string{"list"}, cliOpts())
	if !handled || code != 0 || !strings.Contains(stdout.String(), "No packages installed.") {
		t.Fatalf("empty list = (%v, %d) out=%q err=%q", handled, code, stdout.String(), stderr.String())
	}

	// Global install succeeds.
	stdout.Reset()
	stderr.Reset()
	handled, code = HandlePackageCommand(context.Background(), []string{"install", "npm:cli-demo"}, cliOpts())
	if !handled || code != 0 || !strings.Contains(stdout.String(), "Installed npm:cli-demo") {
		t.Fatalf("install global = (%v, %d) out=%q err=%q", handled, code, stdout.String(), stderr.String())
	}

	// Project local install rejected with --no-approve, allowed with --approve.
	stderr.Reset()
	handled, code = HandlePackageCommand(context.Background(), []string{"install", "npm:@cli/local-pkg", "-l", "--no-approve"}, cliOpts())
	if !handled || code != 1 || !strings.Contains(stderr.String(), "Project is not trusted") {
		t.Fatalf("install -l --no-approve = (%v, %d) err=%q", handled, code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	handled, code = HandlePackageCommand(context.Background(), []string{"install", "npm:@cli/local-pkg", "-l", "--approve"}, cliOpts())
	if !handled || code != 0 || !strings.Contains(stdout.String(), "Installed npm:@cli/local-pkg") {
		t.Fatalf("install -l --approve = (%v, %d) out=%q err=%q", handled, code, stdout.String(), stderr.String())
	}

	// List shows both User and Project packages when approved.
	stdout.Reset()
	stderr.Reset()
	handled, code = HandlePackageCommand(context.Background(), []string{"list", "--approve"}, cliOpts())
	outStr := stdout.String()
	if !handled || code != 0 || !strings.Contains(outStr, "User packages:") || !strings.Contains(outStr, "npm:cli-demo") || !strings.Contains(outStr, "Project packages:") || !strings.Contains(outStr, "npm:@cli/local-pkg") {
		t.Fatalf("list = (%v, %d) out=%q err=%q", handled, code, outStr, stderr.String())
	}

	// Publish v1.2.0 and update --extensions.
	verMu.Lock()
	latestVer = "1.2.0"
	verMu.Unlock()
	stdout.Reset()
	stderr.Reset()
	handled, code = HandlePackageCommand(context.Background(), []string{"update", "--extensions"}, cliOpts())
	if !handled || code != 0 || !strings.Contains(stdout.String(), "Updated 1 package:") || !strings.Contains(stdout.String(), "npm:cli-demo 1.0.0 -> 1.2.0") {
		t.Fatalf("update --extensions = (%v, %d) out=%q err=%q", handled, code, stdout.String(), stderr.String())
	}

	// Re-running update --extension npm:cli-demo prints up-to-date message.
	stdout.Reset()
	stderr.Reset()
	handled, code = HandlePackageCommand(context.Background(), []string{"update", "--extension", "npm:cli-demo"}, cliOpts())
	if !handled || code != 0 || !strings.Contains(stdout.String(), "npm:cli-demo is already up to date") {
		t.Fatalf("update --extension already up to date = (%v, %d) out=%q err=%q", handled, code, stdout.String(), stderr.String())
	}

	// Remove missing package exits 1 with clean message; uninstall existing succeeds.
	stderr.Reset()
	handled, code = HandlePackageCommand(context.Background(), []string{"remove", "npm:missing"}, cliOpts())
	if !handled || code != 1 || !strings.Contains(stderr.String(), "No matching package found for npm:missing") {
		t.Fatalf("remove missing = (%v, %d) err=%q", handled, code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	handled, code = HandlePackageCommand(context.Background(), []string{"uninstall", "npm:cli-demo"}, cliOpts())
	if !handled || code != 0 || !strings.Contains(stdout.String(), "Removed npm:cli-demo") {
		t.Fatalf("uninstall = (%v, %d) out=%q err=%q", handled, code, stdout.String(), stderr.String())
	}

	// Offline `update --extensions` reports skipped cleanly.
	t.Setenv("PI_OFFLINE", "1")
	stdout.Reset()
	stderr.Reset()
	handled, code = HandlePackageCommand(context.Background(), []string{"update", "--extensions"}, cliOpts())
	if !handled || code != 0 || !strings.Contains(stdout.String(), "Skipped package updates (offline mode)") {
		t.Fatalf("offline update --extensions = (%v, %d) out=%q err=%q", handled, code, stdout.String(), stderr.String())
	}

	// Bare `update` prints Extensions skipped note and fails self-update cleanly while offline.
	stdout.Reset()
	stderr.Reset()
	handled, code = HandlePackageCommand(context.Background(), []string{"update"}, cliOpts())
	if !handled || code != 1 || !strings.Contains(stdout.String(), "Extensions are skipped") || !strings.Contains(stderr.String(), "Could not determine latest") {
		t.Fatalf("bare offline update = (%v, %d) out=%q err=%q", handled, code, stdout.String(), stderr.String())
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
	modelAgentDir := t.TempDir()
	authPath := filepath.Join(modelAgentDir, "auth.json")
	modelsPath := filepath.Join(modelAgentDir, "models.json")
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
		AgentDir:     modelAgentDir,
		Stdout:       &stdout,
		Stderr:       &stderr,
		ModelRuntime: rt,
	})
	if !handled || code != 0 || !strings.Contains(stdout.String(), "Model catalogs refreshed") {
		t.Fatalf("update --models = (%v, %d) out=%q err=%q", handled, code, stdout.String(), stderr.String())
	}
}
