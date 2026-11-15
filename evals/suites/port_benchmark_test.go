package suites_test

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha1"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/client"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/extensions"
	"github.com/keejkrej/pi-go/codingagent/extensions/codemodeext"
	"github.com/keejkrej/pi-go/codingagent/extensions/mcpext"
	"github.com/keejkrej/pi-go/codingagent/extensions/toolsearchext"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/modes/rpc"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/storage/jsonl"
	"github.com/keejkrej/pi-go/durable/storage/sqlite"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
	"github.com/keejkrej/pi-go/mcp"
	mcpprotocol "github.com/keejkrej/pi-go/mcp/protocol"
	"github.com/keejkrej/pi-go/protocol"
	"github.com/keejkrej/pi-go/server"
	"github.com/keejkrej/pi-go/server/servertest"
	"github.com/keejkrej/pi-go/server/transports/unix"
	"github.com/keejkrej/pi-go/tui"
)

func tpbenchStrPtr(s string) *string { return &s }
func tpbenchIntPtr(n int) *int       { return &n }
func tpbenchBoolPtr(b bool) *bool    { return &b }

func tpbenchJSONObject(m map[string]any) *jsonx.Object {
	raw, _ := jsonx.Marshal(m)
	v, _ := jsonx.Parse(raw)
	if obj, ok := v.(*jsonx.Object); ok {
		return obj
	}
	return jsonx.NewObject()
}

type tpbenchFauxHarness struct {
	Cwd          string
	AgentDir     string
	Session      *core.AgentSession
	Faux         *providers.FauxProviderHandle
	ModelRuntime *core.ModelRuntime
	Model        *ai.Model
}

func tpbenchNewFauxSession(tb testing.TB, opts *core.CreateAgentSessionOptions) *tpbenchFauxHarness {
	tb.Helper()
	tb.Setenv("PI_OFFLINE", "1")
	ctx := context.Background()
	tmpDir := tb.TempDir()
	cwd := filepath.Join(tmpDir, "work")
	agentDir := filepath.Join(tmpDir, "agent")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		tb.Fatalf("mkdir work: %v", err)
	}
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		tb.Fatalf("mkdir agent: %v", err)
	}

	modelsPath := filepath.Join(agentDir, "models.json")
	rt, err := core.CreateModelRuntime(ctx, &core.CreateModelRuntimeOptions{
		Credentials:       ai.NewInMemoryCredentialStore(),
		ModelsStore:       core.NewInMemoryCodingAgentModelsStore(),
		ModelsPath:        &modelsPath,
		AllowModelNetwork: false,
	})
	if err != nil {
		tb.Fatalf("CreateModelRuntime: %v", err)
	}

	faux, err := providers.FauxProvider(&providers.RegisterFauxProviderOptions{
		Models: []providers.FauxModelDefinition{
			{
				Id:            "faux-bench-1",
				Name:          tpbenchStrPtr("Faux Bench 1"),
				ContextWindow: tpbenchIntPtr(64000),
				MaxTokens:     tpbenchIntPtr(4096),
				Input:         []string{"text"},
			},
		},
	})
	if err != nil {
		rt.Close()
		tb.Fatalf("FauxProvider: %v", err)
	}
	if err := rt.RegisterNativeProvider(faux.Provider); err != nil {
		rt.Close()
		tb.Fatalf("RegisterNativeProvider: %v", err)
	}
	if _, err := rt.Refresh(ctx, &ai.ModelsRefreshOptions{AllowNetwork: tpbenchBoolPtr(false)}); err != nil {
		rt.Close()
		tb.Fatalf("ModelRuntime.Refresh: %v", err)
	}

	model := faux.Models[0]
	var cfg core.CreateAgentSessionOptions
	if opts != nil {
		cfg = *opts
	}
	if cfg.Cwd == "" {
		cfg.Cwd = cwd
	}
	if cfg.AgentDir == "" {
		cfg.AgentDir = agentDir
	}
	if cfg.ModelRuntime == nil {
		cfg.ModelRuntime = rt
	}
	if cfg.Model == nil {
		cfg.Model = model
	}

	res, err := core.CreateAgentSession(ctx, &cfg)
	if err != nil {
		rt.Close()
		tb.Fatalf("CreateAgentSession: %v", err)
	}

	return &tpbenchFauxHarness{
		Cwd:          cfg.Cwd,
		AgentDir:     cfg.AgentDir,
		Session:      res.Session,
		Faux:         faux,
		ModelRuntime: rt,
		Model:        model,
	}
}

func (h *tpbenchFauxHarness) Close() {
	if h == nil {
		return
	}
	if h.Session != nil {
		h.Session.Dispose()
	}
	if h.ModelRuntime != nil {
		h.ModelRuntime.Close()
	}
}

func tpbenchCollectQuickJSLoops() string {
	buf := make([]byte, 128*1024)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			stack := string(buf[:n])
			var leaked []string
			for _, block := range strings.Split(stack, "\n\n") {
				if strings.Contains(block, "(*eqjsVM).loop") {
					leaked = append(leaked, block)
				}
			}
			return strings.Join(leaked, "\n\n")
		}
		buf = make([]byte, len(buf)*2)
	}
}

func tpbenchAssertNoQuickJSGoroutines(tb testing.TB, timeout time.Duration) {
	tb.Helper()
	deadline := time.Now().Add(timeout)
	for {
		leaked := tpbenchCollectQuickJSLoops()
		if leaked == "" {
			return
		}
		if time.Now().After(deadline) {
			tb.Fatalf("leaked QuickJS VM goroutine(s) after Dispose:\n%s", leaked)
		}
		runtime.Gosched()
		time.Sleep(10 * time.Millisecond)
	}
}

// ============================================================================
// Subsystem 1: QuickJS + esbuild .ts user extension lifecycle & goroutine hygiene
// ============================================================================

const tpbenchLifecycleExtensionTS = `
import { VERSION, formatSize, type ExtensionAPI } from "@mariozechner/pi-coding-agent";
import { Type } from "@sinclair/typebox";
import { truncateToWidth, visibleWidth } from "@mariozechner/pi-tui";
import { StringEnum, uuidv7 } from "@mariozechner/pi-ai";

interface FmtPayload {
  tag: string;
  formatted: string;
  width: number;
  bytesLabel: string;
  version: string;
  callUuid: string;
}

export default function (pi: ExtensionAPI) {
  pi.registerTool({
    name: "bench_fmt",
    label: "Bench Formatter",
    description: "Formats text with TypeBox + TUI + AI helpers",
    parameters: Type.Object({
      text: Type.String(),
      caseMode: StringEnum(["upper", "lower"] as const),
    }),
    async execute(_toolCallId, params) {
      const transformed = params.caseMode === "upper"
        ? String(params.text).toUpperCase()
        : String(params.text).toLowerCase();
      const clipped = truncateToWidth(transformed, 24, "...");
      const payload: FmtPayload = {
        tag: "bench-fmt-ok",
        formatted: clipped,
        width: visibleWidth(clipped),
        bytesLabel: formatSize(clipped.length),
        version: VERSION,
        callUuid: uuidv7(),
      };
      return {
        content: [{ type: "text", text: payload.tag + ":" + payload.formatted + "|w=" + String(payload.width) + "|" + payload.bytesLabel }],
        details: payload,
      };
    },
  });

  pi.registerTool({
    name: "blocked_policy_tool",
    label: "Blocked Policy Tool",
    description: "Exists so tool_call hook can intercept and block it",
    parameters: Type.Object({ text: Type.String() }),
    async execute() {
      return { content: [{ type: "text", text: "UNEXPECTED_EXECUTION" }] };
    },
  });

  pi.registerCommand("extcheck", {
    description: "Records extension command execution into session entries",
    async handler(args: string, ctx: any) {
      ctx.ui.setStatus("extcheck", "ran:" + args);
      pi.appendEntry("extcheck-entry", {
        arg: args,
        version: VERSION,
        id: uuidv7(),
      });
    },
  });

  pi.on("tool_call", async (event: any) => {
    if (event.toolName === "blocked_policy_tool") {
      return { block: true, reason: "policy-blocked" };
    }
    if (event.toolName === "bench_fmt" && event.input && typeof event.input.text === "string") {
      event.input.text = event.input.text + ":hooked";
    }
  });

  pi.on("before_agent_start", async (event: any) => {
    return {
      systemPrompt: String(event.systemPrompt || "") + " [EXT_SYSTEM_MARKER]",
      message: {
        customType: "ext-boot",
        content: "boot-injected",
        display: false,
      },
    };
  });
}
`

func TestPortConformance_QuickJSLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Guarantee clean baseline before spawning our QuickJS VM.
	tpbenchAssertNoQuickJSGoroutines(t, 2*time.Second)

	extDir := t.TempDir()
	extPath := filepath.Join(extDir, "lifecycle_ext.ts")
	if err := os.WriteFile(extPath, []byte(tpbenchLifecycleExtensionTS), 0o600); err != nil {
		t.Fatalf("write .ts extension: %v", err)
	}

	h := tpbenchNewFauxSession(t, &core.CreateAgentSessionOptions{
		NoExtensions:             true,
		NoSkills:                 true,
		NoPromptTemplates:        true,
		NoThemes:                 true,
		NoContextFiles:           true,
		AdditionalExtensionPaths: []string{extPath},
	})
	closed := false
	defer func() {
		if !closed {
			h.Close()
		}
	}()

	// 1. Slash command execution via Prompt("/extcheck ...")
	if err := h.Session.Prompt(ctx, "/extcheck smoke-arg"); err != nil {
		t.Fatalf("slash command Prompt(/extcheck): %v", err)
	}
	foundCmdEntry := false
	for _, entry := range h.Session.SessionManager.GetEntries() {
		if ce, ok := entry.(*core.CustomEntry); ok && ce.CustomType == "extcheck-entry" {
			dataMap, _ := ce.Data.(map[string]any)
			if dataMap["arg"] == "smoke-arg" && dataMap["version"] != "" && dataMap["id"] != "" {
				foundCmdEntry = true
			}
		}
	}
	if !foundCmdEntry {
		t.Fatalf("expected extcheck-entry recorded in session manager, got entries=%d", len(h.Session.SessionManager.GetEntries()))
	}

	// 2. Active tools include extension custom tool.
	activeTools := h.Session.GetActiveToolNames()
	hasBenchFmt := false
	for _, name := range activeTools {
		if name == "bench_fmt" {
			hasBenchFmt = true
			break
		}
	}
	if !hasBenchFmt {
		t.Fatalf("expected bench_fmt in active tools %v", activeTools)
	}

	// 3. Turn flow exercising before_agent_start, tool_call mutation & blocking, and bench_fmt execution.
	var mu sync.Mutex
	var capturedSystem string
	var capturedToolResults []string
	var blockedToolResults []string

	h.Faux.SetResponses([]providers.FauxResponseStep{
		// Step 1: Request bench_fmt tool call
		&providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
				providers.FauxToolCall("bench_fmt", tpbenchJSONObject(map[string]any{
					"text":     "alpha",
					"caseMode": "upper",
				}), &providers.FauxToolCallOptions{Id: tpbenchStrPtr("call_fmt_1")}),
				providers.FauxToolCall("blocked_policy_tool", tpbenchJSONObject(map[string]any{
					"text": "secret",
				}), &providers.FauxToolCallOptions{Id: tpbenchStrPtr("call_block_1")}),
			}, nil),
		},
		// Step 2: Inspect transcript passed by AgentSession to provider
		providers.FauxResponseFactory(func(_ context.Context, transcript *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
			mu.Lock()
			defer mu.Unlock()
			if sm := ai.GetCurrentSystemMessage(transcript.Messages); sm != nil && sm.Content.Text != nil {
				capturedSystem = *sm.Content.Text
			}
			for _, m := range transcript.Messages {
				tr, ok := m.(*ai.ToolResultMessage)
				if !ok {
					continue
				}
				var sb strings.Builder
				for _, b := range tr.Content {
					if tc, ok := b.(*ai.TextContent); ok {
						sb.WriteString(tc.Text)
					}
				}
				switch tr.ToolName {
				case "bench_fmt":
					capturedToolResults = append(capturedToolResults, sb.String())
				case "blocked_policy_tool":
					blockedToolResults = append(blockedToolResults, sb.String())
				}
			}
			return providers.FauxAssistantMessage(providers.FauxAssistantText("lifecycle-complete"), nil), nil
		}),
	})

	if err := h.Session.Prompt(ctx, "Run bench_fmt now"); err != nil {
		t.Fatalf("Prompt tool turn: %v", err)
	}

	mu.Lock()
	sysPrompt := capturedSystem
	fmtResults := append([]string(nil), capturedToolResults...)
	blkResults := append([]string(nil), blockedToolResults...)
	mu.Unlock()

	if !strings.Contains(sysPrompt, "[EXT_SYSTEM_MARKER]") {
		t.Fatalf("expected before_agent_start system prompt marker, got %q", sysPrompt)
	}
	if len(fmtResults) != 1 || !strings.Contains(fmtResults[0], "bench-fmt-ok:ALPHA:HOOKED|w=12|12 B") {
		t.Fatalf("expected bench_fmt output with tool_call mutation 'ALPHA:HOOKED', got %v", fmtResults)
	}
	if len(blkResults) != 1 || !strings.Contains(blkResults[0], "policy-blocked") {
		t.Fatalf("expected tool_call block reason 'policy-blocked', got %v", blkResults)
	}

	// Verify before_agent_start injected custom message into session tree.
	foundBootMsg := false
	for _, entry := range h.Session.SessionManager.GetEntries() {
		if cme, ok := entry.(*core.CustomMessageEntry); ok && cme.CustomType == "ext-boot" {
			foundBootMsg = true
		}
	}
	if !foundBootMsg {
		t.Fatalf("expected before_agent_start customMessage entry 'ext-boot' in session")
	}

	// Verify QuickJS loop goroutine actually ran while active.
	if activeLoops := tpbenchCollectQuickJSLoops(); activeLoops == "" {
		t.Fatalf("expected active (*eqjsVM).loop goroutine before Dispose")
	}

	// 4. Dispose and ensure zero leaked QuickJS goroutines.
	h.Close()
	closed = true
	tpbenchAssertNoQuickJSGoroutines(t, 3*time.Second)
}

func BenchmarkPort_QuickJSTranspileAndExecute(b *testing.B) {
	extDir := b.TempDir()
	extPath := filepath.Join(extDir, "lifecycle_ext.ts")
	if err := os.WriteFile(extPath, []byte(tpbenchLifecycleExtensionTS), 0o600); err != nil {
		b.Fatalf("write .ts extension: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h := tpbenchNewFauxSession(b, &core.CreateAgentSessionOptions{
			NoExtensions:             true,
			NoSkills:                 true,
			NoPromptTemplates:        true,
			NoThemes:                 true,
			NoContextFiles:           true,
			AdditionalExtensionPaths: []string{extPath},
		})
		runner := h.Session.ExtensionRunner()
		if runner == nil {
			h.Close()
			b.Fatalf("expected extension runner")
		}
		def := runner.GetToolDefinition("bench_fmt")
		if def == nil {
			h.Close()
			b.Fatalf("expected bench_fmt tool")
		}
		_, err := def.Execute(context.Background(), "bench-call", tpbenchJSONObject(map[string]any{
			"text":     "hello",
			"caseMode": "upper",
		}), nil, runner.CreateToolContext("bench-call", context.Background()))
		if err != nil {
			h.Close()
			b.Fatalf("bench_fmt execute: %v", err)
		}
		h.Close()
	}
}

// ============================================================================
// Subsystem 2: DefaultPackageManager (local, bare git, mock npm, offline)
// ============================================================================

type tpbenchTarEntry struct {
	name    string
	content string
}

func tpbenchBuildTgz(tb testing.TB, entries []tpbenchTarEntry) []byte {
	tb.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for _, e := range entries {
		hdr := &tar.Header{
			Name:     e.name,
			Typeflag: tar.TypeReg,
			Mode:     0o644,
			Size:     int64(len(e.content)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			tb.Fatalf("WriteHeader(%s): %v", e.name, err)
		}
		if len(e.content) > 0 {
			if _, err := tw.Write([]byte(e.content)); err != nil {
				tb.Fatalf("Write(%s): %v", e.name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		tb.Fatalf("tar Close: %v", err)
	}
	if err := gw.Close(); err != nil {
		tb.Fatalf("gzip Close: %v", err)
	}
	return buf.Bytes()
}

type tpbenchNpmVersion struct {
	version string
	tgz     []byte
	deps    map[string]string
}

type tpbenchNpmRegistry struct {
	mu       sync.Mutex
	server   *httptest.Server
	packages map[string]struct {
		distTags map[string]string
		versions map[string]tpbenchNpmVersion
	}
	requests []string
}

func tpbenchNewNpmRegistry(tb testing.TB) *tpbenchNpmRegistry {
	tb.Helper()
	reg := &tpbenchNpmRegistry{
		packages: make(map[string]struct {
			distTags map[string]string
			versions map[string]tpbenchNpmVersion
		}),
	}
	reg.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reg.mu.Lock()
		reg.requests = append(reg.requests, r.URL.Path)
		reg.mu.Unlock()

		reqPath := strings.TrimPrefix(r.URL.Path, "/")
		if strings.HasPrefix(reqPath, "tarballs/") {
			key := strings.TrimPrefix(reqPath, "tarballs/")
			reg.mu.Lock()
			var found *tpbenchNpmVersion
			for pkgName, data := range reg.packages {
				safePkg := strings.ReplaceAll(pkgName, "/", "__")
				for ver, vf := range data.versions {
					if key == safePkg+"__"+ver+".tgz" {
						vfCopy := vf
						found = &vfCopy
						break
					}
				}
				if found != nil {
					break
				}
			}
			reg.mu.Unlock()
			if found == nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(found.tgz)
			return
		}

		reg.mu.Lock()
		pkgData, ok := reg.packages[reqPath]
		if !ok {
			reg.mu.Unlock()
			http.NotFound(w, r)
			return
		}
		versionsObj := make(map[string]any, len(pkgData.versions))
		for ver, vf := range pkgData.versions {
			safePkg := strings.ReplaceAll(reqPath, "/", "__")
			tarURL := fmt.Sprintf("%s/tarballs/%s__%s.tgz", reg.server.URL, safePkg, ver)
			sum1 := sha1.Sum(vf.tgz)
			sum512 := sha512.Sum512(vf.tgz)
			entry := map[string]any{
				"name":    reqPath,
				"version": ver,
				"dist": map[string]any{
					"tarball":   tarURL,
					"shasum":    hex.EncodeToString(sum1[:]),
					"integrity": "sha512-" + base64.StdEncoding.EncodeToString(sum512[:]),
				},
			}
			if len(vf.deps) > 0 {
				entry["dependencies"] = vf.deps
			}
			versionsObj[ver] = entry
		}
		distTagsCopy := make(map[string]string, len(pkgData.distTags))
		for k, v := range pkgData.distTags {
			distTagsCopy[k] = v
		}
		reg.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if payload, err := jsonx.Marshal(map[string]any{
			"name":      reqPath,
			"dist-tags": distTagsCopy,
			"versions":  versionsObj,
		}); err == nil {
			_, _ = w.Write(payload)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}))
	tb.Cleanup(reg.server.Close)
	return reg
}

func (r *tpbenchNpmRegistry) publish(pkgName, distTag string, vf tpbenchNpmVersion) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry := r.packages[pkgName]
	if entry.distTags == nil {
		entry.distTags = make(map[string]string)
	}
	if entry.versions == nil {
		entry.versions = make(map[string]tpbenchNpmVersion)
	}
	entry.versions[vf.version] = vf
	if distTag != "" {
		entry.distTags[distTag] = vf.version
	}
	r.packages[pkgName] = entry
}

func (r *tpbenchNpmRegistry) requestCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

func tpbenchWriteFile(tb testing.TB, path, content string) {
	tb.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		tb.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		tb.Fatalf("write %s: %v", path, err)
	}
}

func tpbenchRunGit(tb testing.TB, dir string, args ...string) string {
	tb.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		tb.Fatalf("git %v in %s failed: %v\n%s", args, dir, err, string(out))
	}
	return strings.TrimSpace(string(out))
}

func TestPortConformance_PackageManager(t *testing.T) {
	ctx := context.Background()
	reg := tpbenchNewNpmRegistry(t)
	t.Setenv("NPM_CONFIG_REGISTRY", reg.server.URL)
	t.Setenv("PI_OFFLINE", "")

	tmpDir := t.TempDir()
	workDir := filepath.Join(tmpDir, "work")
	agentDir := filepath.Join(tmpDir, "agent")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("mkdir work: %v", err)
	}
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("mkdir agent: %v", err)
	}

	sm := core.SettingsManagerInMemory()
	sm.SetProjectTrusted(true)
	pm := core.NewDefaultPackageManager(core.DefaultPackageManagerOptions{
		Cwd:             workDir,
		AgentDir:        agentDir,
		SettingsManager: sm,
	})

	// (a) Local directory package with pi manifest resource filters + settings filter.
	localPkgDir := filepath.Join(tmpDir, "local-pkg")
	tpbenchWriteFile(t, filepath.Join(localPkgDir, "extensions", "keep.ts"), "export default function() {}")
	tpbenchWriteFile(t, filepath.Join(localPkgDir, "extensions", "excluded.ts"), "export default function() {}")
	tpbenchWriteFile(t, filepath.Join(localPkgDir, "skills", "good-skill", "SKILL.md"), "# Good Skill")
	tpbenchWriteFile(t, filepath.Join(localPkgDir, "skills", "skip-skill", "SKILL.md"), "# Skip Skill")
	tpbenchWriteFile(t, filepath.Join(localPkgDir, "prompts", "brief.md"), "# Brief Prompt")
	tpbenchWriteFile(t, filepath.Join(localPkgDir, "package.json"), `{
		"name": "local-pkg",
		"version": "1.0.0",
		"pi": {
			"extensions": ["extensions/*.ts", "!extensions/excluded.ts"],
			"skills": ["skills/*/SKILL.md"],
			"prompts": ["prompts/*.md"]
		}
	}`)
	sm.SetPackages([]core.PackageSource{
		core.PackageSourceFromObject(core.PackageSourceObject{
			Source: localPkgDir,
			Skills: []string{"-**/skip-skill/**"},
		}),
	})

	// (b) Local bare git repo (git init --bare) + seed working repo + URL rewrite.
	bareDir := filepath.Join(tmpDir, "bare-ext.git")
	seedDir := filepath.Join(tmpDir, "seed-ext")
	tpbenchRunGit(t, "", "init", "--bare", bareDir)
	tpbenchRunGit(t, "", "init", seedDir)
	tpbenchRunGit(t, seedDir, "config", "user.email", "evals@example.com")
	tpbenchRunGit(t, seedDir, "config", "user.name", "Evals Bench")
	tpbenchWriteFile(t, filepath.Join(seedDir, "package.json"), `{
		"name": "bare-git-ext",
		"version": "1.0.0",
		"pi": { "extensions": ["extensions/git_tool.ts"] }
	}`)
	tpbenchWriteFile(t, filepath.Join(seedDir, "extensions", "git_tool.ts"), "// v1")
	tpbenchRunGit(t, seedDir, "add", "-A")
	tpbenchRunGit(t, seedDir, "commit", "-m", "v1")
	tpbenchRunGit(t, seedDir, "branch", "-M", "main")
	tpbenchRunGit(t, seedDir, "push", bareDir, "main")
	tpbenchRunGit(t, bareDir, "symbolic-ref", "HEAD", "refs/heads/main")
	gitCommit1 := tpbenchRunGit(t, seedDir, "rev-parse", "HEAD")

	bareRemoteURL := "https://git.example.test/evals/bare-ext"
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", fmt.Sprintf("url.%s.insteadOf", bareDir))
	t.Setenv("GIT_CONFIG_VALUE_0", bareRemoteURL)

	if err := pm.InstallAndPersist(ctx, bareRemoteURL); err != nil {
		t.Fatalf("InstallAndPersist bare git: %v", err)
	}
	gitInstalled := pm.GetInstalledPath(bareRemoteURL)
	if gitInstalled == nil {
		t.Fatalf("expected installed path for %s", bareRemoteURL)
	}
	if head := tpbenchRunGit(t, *gitInstalled, "rev-parse", "HEAD"); head != gitCommit1 {
		t.Fatalf("git clone HEAD = %s, want %s", head, gitCommit1)
	}

	// (c) Mock httptest npm registry with semver range (^1.0.0) & upgrades.
	depV1 := tpbenchBuildTgz(t, []tpbenchTarEntry{
		{name: "package/package.json", content: `{"name":"bench-dep","version":"1.0.0"}`},
		{name: "package/index.js", content: "module.exports = 'dep-v1';\n"},
	})
	reg.publish("bench-dep", "latest", tpbenchNpmVersion{version: "1.0.0", tgz: depV1})

	pkg100 := tpbenchBuildTgz(t, []tpbenchTarEntry{
		{name: "package/package.json", content: `{"name":"bench-pkg","version":"1.0.0","dependencies":{"bench-dep":"^1.0.0"},"pi":{"extensions":["extensions/*.ts","!extensions/drop.ts"]}}`},
		{name: "package/extensions/main.ts", content: "// 1.0.0"},
		{name: "package/extensions/drop.ts", content: "// excluded by manifest"},
	})
	reg.publish("bench-pkg", "latest", tpbenchNpmVersion{
		version: "1.0.0",
		tgz:     pkg100,
		deps:    map[string]string{"bench-dep": "^1.0.0"},
	})

	if err := pm.InstallAndPersist(ctx, "npm:bench-pkg@^1.0.0", &core.PackageInstallOptions{Local: true}); err != nil {
		t.Fatalf("InstallAndPersist npm:bench-pkg@^1.0.0: %v", err)
	}
	npmInstalled := pm.GetInstalledPath("npm:bench-pkg@^1.0.0", "project")
	if npmInstalled == nil {
		t.Fatalf("expected installed project path for npm:bench-pkg@^1.0.0")
	}

	// Verify list/resolve across local, bare git, and npm packages + manifest resource filters.
	configured := pm.GetInstalledPackages()
	if len(configured) != 3 {
		t.Fatalf("GetInstalledPackages len = %d, want 3 (%+v)", len(configured), configured)
	}
	resolved, err := pm.Resolve(ctx)
	if err != nil {
		t.Fatalf("pm.Resolve: %v", err)
	}
	var extPaths []string
	for _, r := range resolved.Extensions {
		if r.Enabled {
			extPaths = append(extPaths, r.Path)
		}
	}
	for _, want := range []string{
		filepath.Join(localPkgDir, "extensions", "keep.ts"),
		filepath.Join(*gitInstalled, "extensions", "git_tool.ts"),
		filepath.Join(*npmInstalled, "extensions", "main.ts"),
	} {
		if !slices.Contains(extPaths, want) {
			t.Fatalf("expected enabled extension %s in %v", want, extPaths)
		}
	}
	for _, excluded := range []string{
		filepath.Join(localPkgDir, "extensions", "excluded.ts"),
		filepath.Join(*npmInstalled, "extensions", "drop.ts"),
	} {
		if slices.Contains(extPaths, excluded) {
			t.Fatalf("manifest-excluded extension %s should not be resolved in %v", excluded, extPaths)
		}
	}
	var goodSkillEnabled, skipSkillEnabled bool
	for _, s := range resolved.Skills {
		if strings.Contains(s.Path, "good-skill") {
			goodSkillEnabled = s.Enabled
		}
		if strings.Contains(s.Path, "skip-skill") {
			skipSkillEnabled = s.Enabled
		}
	}
	if !goodSkillEnabled || skipSkillEnabled {
		t.Fatalf("unexpected skill filter state: good=%v skip=%v (skills=%+v)", goodSkillEnabled, skipSkillEnabled, resolved.Skills)
	}

	// Publish npm 1.2.0 (in range ^1.0.0) and 2.0.0 (out of range ^1.0.0) + push git commit 2.
	pkg120 := tpbenchBuildTgz(t, []tpbenchTarEntry{
		{name: "package/package.json", content: `{"name":"bench-pkg","version":"1.2.0","pi":{"extensions":["extensions/*.ts","!extensions/drop.ts"]}}`},
		{name: "package/extensions/main.ts", content: "// 1.2.0"},
	})
	pkg200 := tpbenchBuildTgz(t, []tpbenchTarEntry{
		{name: "package/package.json", content: `{"name":"bench-pkg","version":"2.0.0"}`},
		{name: "package/extensions/main.ts", content: "// 2.0.0"},
	})
	reg.publish("bench-pkg", "", tpbenchNpmVersion{version: "1.2.0", tgz: pkg120})
	reg.publish("bench-pkg", "latest", tpbenchNpmVersion{version: "2.0.0", tgz: pkg200})

	tpbenchWriteFile(t, filepath.Join(seedDir, "extensions", "git_tool.ts"), "// v2")
	tpbenchRunGit(t, seedDir, "add", "-A")
	tpbenchRunGit(t, seedDir, "commit", "-m", "v2")
	tpbenchRunGit(t, seedDir, "push", bareDir, "main")
	gitCommit2 := tpbenchRunGit(t, seedDir, "rev-parse", "HEAD")

	updates, err := pm.CheckForAvailableUpdates(ctx)
	if err != nil {
		t.Fatalf("CheckForAvailableUpdates: %v", err)
	}
	if len(updates) != 2 {
		t.Fatalf("expected 2 updates (bare git + npm ^1.0.0), got %+v", updates)
	}
	if err := pm.Update(ctx); err != nil {
		t.Fatalf("pm.Update: %v", err)
	}
	if head := tpbenchRunGit(t, *gitInstalled, "rev-parse", "HEAD"); head != gitCommit2 {
		t.Fatalf("git HEAD after Update = %s, want %s", head, gitCommit2)
	}
	pkgBytes, err := os.ReadFile(filepath.Join(*npmInstalled, "package.json"))
	if err != nil {
		t.Fatalf("read updated npm package.json: %v", err)
	}
	if !strings.Contains(string(pkgBytes), `"1.2.0"`) || strings.Contains(string(pkgBytes), `"2.0.0"`) {
		t.Fatalf("expected semver range ^1.0.0 to update to 1.2.0 (not 2.0.0), got %s", string(pkgBytes))
	}

	// Remove npm and bare git packages cleanly.
	if removed, err := pm.RemoveAndPersist(ctx, "npm:bench-pkg@^1.0.0", &core.PackageRemoveOptions{Local: true}); err != nil || !removed {
		t.Fatalf("RemoveAndPersist npm = (%v, %v)", removed, err)
	}
	if removed, err := pm.RemoveAndPersist(ctx, bareRemoteURL); err != nil || !removed {
		t.Fatalf("RemoveAndPersist git = (%v, %v)", removed, err)
	}
	if remaining := pm.GetInstalledPackages(); len(remaining) != 1 {
		t.Fatalf("expected only local-pkg remaining after removals, got %+v", remaining)
	}

	// PI_OFFLINE=1 enforcement: must refuse network npm & git installs without touching HTTP server.
	t.Setenv("PI_OFFLINE", "1")
	reqsBefore := reg.requestCount()
	if err := pm.Install(ctx, "npm:bench-pkg"); err == nil || !strings.Contains(strings.ToLower(err.Error()), "offline") {
		t.Fatalf("expected offline error on npm Install with PI_OFFLINE=1, got %v", err)
	}
	if err := pm.Install(ctx, "https://git.example.test/evals/offline-repo"); err == nil || !strings.Contains(strings.ToLower(err.Error()), "offline") {
		t.Fatalf("expected offline error on git Install with PI_OFFLINE=1, got %v", err)
	}
	if offlineUpdates, err := pm.CheckForAvailableUpdates(ctx); err != nil || len(offlineUpdates) != 0 {
		t.Fatalf("expected empty updates in offline mode, got (%+v, %v)", offlineUpdates, err)
	}
	if reg.requestCount() != reqsBefore {
		t.Fatalf("expected zero HTTP requests while PI_OFFLINE=1 (before=%d after=%d)", reqsBefore, reg.requestCount())
	}
}

func BenchmarkPort_PackageManagerResolve(b *testing.B) {
	ctx := context.Background()
	tmpDir := b.TempDir()
	workDir := filepath.Join(tmpDir, "work")
	agentDir := filepath.Join(tmpDir, "agent")
	pkgDir := filepath.Join(tmpDir, "bench-pkg")
	tpbenchWriteFile(b, filepath.Join(pkgDir, "extensions", "ext_a.ts"), "export default function() {}")
	tpbenchWriteFile(b, filepath.Join(pkgDir, "extensions", "ext_skip.ts"), "export default function() {}")
	tpbenchWriteFile(b, filepath.Join(pkgDir, "skills", "s1", "SKILL.md"), "# S1")
	tpbenchWriteFile(b, filepath.Join(pkgDir, "prompts", "p1.md"), "# P1")
	tpbenchWriteFile(b, filepath.Join(pkgDir, "package.json"), `{"name":"bench-pkg","pi":{"extensions":["extensions/*.ts","!extensions/ext_skip.ts"]}}`)
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		b.Fatal(err)
	}
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		b.Fatal(err)
	}
	sm := core.SettingsManagerInMemory()
	sm.SetPackages([]core.PackageSource{core.PackageSourceFromString(pkgDir)})
	pm := core.NewDefaultPackageManager(core.DefaultPackageManagerOptions{
		Cwd:             workDir,
		AgentDir:        agentDir,
		SettingsManager: sm,
	})

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := pm.Resolve(ctx); err != nil {
			b.Fatalf("pm.Resolve: %v", err)
		}
	}
}

// ============================================================================
// Subsystem 3: BuiltInExtensions + tool_search + codemode + mcpext (in-memory)
// ============================================================================

func tpbenchObjectSchema(fields ...struct {
	name   string
	schema *typebox.Schema
}) *typebox.Schema {
	props := omap.NewMap[string, *typebox.Schema]()
	for _, f := range fields {
		props.Set(f.name, f.schema)
	}
	return typebox.Object(props)
}

func tpbenchWaitFor(tb testing.TB, timeout time.Duration, cond func() bool) {
	tb.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !cond() {
		tb.Fatalf("condition not met within %s", timeout)
	}
}

func TestPortConformance_BuiltInExtensions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Part A: BuiltInExtensions count & default CreateAgentSession wiring.
	if len(extensions.BuiltInExtensions) != 4 {
		t.Fatalf("extensions.BuiltInExtensions len = %d, want 4", len(extensions.BuiltInExtensions))
	}
	defaultH := tpbenchNewFauxSession(t, &core.CreateAgentSessionOptions{
		NoSkills:          true,
		NoPromptTemplates: true,
		NoThemes:          true,
		NoContextFiles:    true,
	})
	var defaultToolNames []string
	for _, info := range defaultH.Session.GetAllTools() {
		defaultToolNames = append(defaultToolNames, info.Name)
	}
	defaultH.Close()
	for _, wantTool := range []string{codemodeext.CodemodeToolName, toolsearchext.ToolSearchToolName} {
		if !slices.Contains(defaultToolNames, wantTool) {
			t.Fatalf("expected built-in tool %q registered with default BuiltInExtensions, got %v", wantTool, defaultToolNames)
		}
	}

	// Part B: tool_search + codemode (async pipeline & return null) + in-memory mcpext server/client.
	tmpDir := t.TempDir()
	workDir := filepath.Join(tmpDir, "work")
	agentDir := filepath.Join(tmpDir, "agent")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}

	var mcpMu sync.Mutex
	var mcpCalls []string
	var serverTransports []*mcp.InMemoryTransport
	t.Cleanup(func() {
		mcpMu.Lock()
		ts := append([]*mcp.InMemoryTransport(nil), serverTransports...)
		mcpMu.Unlock()
		for _, srv := range ts {
			_ = srv.Close()
		}
	})

	startInMemoryMcp := func() *mcp.InMemoryTransportPair {
		pair := mcp.CreateInMemoryTransportPair()
		pair.Server.OnMessage(func(msg mcpprotocol.JsonRpcMessage) {
			req, ok := msg.(*mcpprotocol.JsonRpcRequest)
			if !ok {
				return
			}
			var resp mcpprotocol.JsonRpcMessage
			switch req.Method {
			case "initialize":
				resp = &mcpprotocol.JsonRpcSuccessResponse{
					Jsonrpc: "2.0",
					Id:      req.Id,
					Result: jsonx.ObjectOf(
						"protocolVersion", mcpprotocol.LatestProtocolVersion,
						"capabilities", jsonx.ObjectOf("tools", jsonx.NewObject()),
						"serverInfo", jsonx.ObjectOf("name", "docs", "version", "1.0.0"),
					),
				}
			case "tools/list":
				resp = &mcpprotocol.JsonRpcSuccessResponse{
					Jsonrpc: "2.0",
					Id:      req.Id,
					Result: jsonx.ObjectOf("tools", []mcpprotocol.Tool{
						{
							Name:        "search",
							Description: tpbenchStrPtr("Search offline documentation entries."),
							InputSchema: jsonx.ObjectOf(
								"type", "object",
								"properties", jsonx.ObjectOf("query", jsonx.ObjectOf("type", "string")),
								"required", []string{"query"},
							),
							OutputSchema: jsonx.ObjectOf(
								"type", "object",
								"properties", jsonx.ObjectOf("hit", jsonx.ObjectOf("type", "string")),
								"required", []string{"hit"},
							),
						},
					}),
				}
			case "tools/call":
				paramsObj, _ := req.Params.(*jsonx.Object)
				tName, _ := paramsObj.GetString("name")
				argsRaw, _ := paramsObj.Get("arguments")
				argsObj, _ := argsRaw.(*jsonx.Object)
				queryStr, _ := argsObj.GetString("query")
				mcpMu.Lock()
				mcpCalls = append(mcpCalls, tName+":"+queryStr)
				mcpMu.Unlock()
				resp = &mcpprotocol.JsonRpcSuccessResponse{
					Jsonrpc: "2.0",
					Id:      req.Id,
					Result: jsonx.ObjectOf(
						"content", []any{jsonx.ObjectOf("type", "text", "text", "doc-hit:"+queryStr)},
						"structuredContent", jsonx.ObjectOf("hit", "doc-hit:"+queryStr),
					),
				}
			default:
				resp = &mcpprotocol.JsonRpcSuccessResponse{Jsonrpc: "2.0", Id: req.Id, Result: jsonx.NewObject()}
			}
			go func() {
				_ = pair.Server.Send(resp)
			}()
		})
		if err := pair.Server.Start(); err != nil {
			t.Fatalf("pair.Server.Start: %v", err)
		}
		mcpMu.Lock()
		serverTransports = append(serverTransports, pair.Server)
		mcpMu.Unlock()
		return pair
	}

	expDirect := core.McpExposureDirect
	mcpEntry := mcpext.McpServerEntry{
		Name: "docs",
		Config: mcpext.McpServerConfig{
			Url:      tpbenchStrPtr("http://in-memory-mcp.local"),
			Exposure: &expDirect,
		},
		Source: "test",
	}

	customPipelineExt := core.InlineExtension{
		Name: "pipeline-tools",
		Factory: func(pi core.ExtensionAPI) error {
			pi.RegisterTool(core.ToolDefinition{
				Name:        "deferred_metrics",
				Label:       "Deferred Metrics",
				Description: "Look up internal telemetry metrics by service key",
				Exposure:    core.ToolExposureDeferred,
				Parameters: tpbenchObjectSchema(struct {
					name   string
					schema *typebox.Schema
				}{"key", typebox.String()}),
				Execute: func(_ context.Context, _ string, input *jsonx.Object, _ agent.AgentToolUpdateCallback, _ core.ExtensionToolContext) (agent.AgentToolResult, error) {
					key, _ := input.GetString("key")
					return agent.AgentToolResult{
						Content:           ai.ContentList{&ai.TextContent{Type: "text", Text: "metric:" + key}},
						StructuredContent: jsonx.ObjectOf("metric", "metric:"+key),
					}, nil
				},
			})
			pi.RegisterTool(core.ToolDefinition{
				Name:        "combine_step",
				Label:       "Combine Step",
				Description: "Combines doc hit and telemetry metric into final summary",
				Exposure:    core.ToolExposureCodemode,
				Parameters: tpbenchObjectSchema(
					struct {
						name   string
						schema *typebox.Schema
					}{"doc", typebox.String()},
					struct {
						name   string
						schema *typebox.Schema
					}{"metric", typebox.String()},
				),
				Execute: func(_ context.Context, _ string, input *jsonx.Object, _ agent.AgentToolUpdateCallback, _ core.ExtensionToolContext) (agent.AgentToolResult, error) {
					docVal, _ := input.GetString("doc")
					metVal, _ := input.GetString("metric")
					joined := docVal + "+" + metVal
					return agent.AgentToolResult{
						Content:           ai.ContentList{&ai.TextContent{Type: "text", Text: "combined:" + joined}},
						StructuredContent: jsonx.ObjectOf("combined", "combined:"+joined),
					}, nil
				},
			})
			return nil
		},
	}

	rl := core.NewDefaultResourceLoader(core.DefaultResourceLoaderOptions{
		Cwd:                       workDir,
		AgentDir:                  agentDir,
		SettingsManager:           core.SettingsManagerInMemory(),
		NoSkills:                  true,
		NoPromptTemplates:         true,
		NoThemes:                  true,
		NoContextFiles:            true,
		DisabledBuiltinExtensions: []string{"mcp"},
		ExtensionFactories: []core.InlineExtension{
			{
				Name: "mcp",
				Factory: mcpext.CreateMcpExtension(mcpext.McpExtensionOptions{
					LoadConfig: func(core.ExtensionContext) mcpext.LoadedMcpConfig {
						return mcpext.LoadedMcpConfig{Servers: []mcpext.McpServerEntry{mcpEntry}}
					},
					CreateTransport: func(mcpext.McpServerEntry, string, mcp.AuthProvider) (mcp.McpTransport, error) {
						return startInMemoryMcp().Client, nil
					},
				}),
			},
			customPipelineExt,
		},
	})
	defer rl.Close()
	if err := rl.Reload(ctx); err != nil {
		t.Fatalf("ResourceLoader.Reload: %v", err)
	}

	h := tpbenchNewFauxSession(t, &core.CreateAgentSessionOptions{
		Cwd:            workDir,
		AgentDir:       agentDir,
		ResourceLoader: rl,
	})
	defer h.Close()

	if err := h.Session.BindExtensions(ctx, core.ExtensionBindings{
		OnError: func(e core.ExtensionError) {
			t.Logf("extension error during builtin test: %+v", e)
		},
	}); err != nil {
		t.Fatalf("BindExtensions: %v", err)
	}

	// Wait for async in-memory MCP handshake & tool registration.
	tpbenchWaitFor(t, 5*time.Second, func() bool {
		for _, info := range h.Session.GetAllTools() {
			if info.Name == "mcp__docs__search" {
				return true
			}
		}
		return false
	})

	// Activate tool_search, codemode, combine_step, and mcp__docs__search; keep deferred_metrics unloaded initially.
	h.Session.SetActiveToolsByName([]string{
		"read",
		toolsearchext.ToolSearchToolName,
		codemodeext.CodemodeToolName,
		"combine_step",
		"mcp__docs__search",
	})
	if slices.Contains(h.Session.GetActiveToolNames(), "deferred_metrics") {
		t.Fatalf("deferred_metrics should be inactive before tool_search")
	}

	var toolSearchOutput string
	var pipelineOutput string
	var nullOutput string
	var turn2DeclaredDeferred bool

	stopTool := ai.StopReasonToolUse
	stopDone := ai.StopReasonStop
	pipelineScript := `
const doc = await tools.mcp__docs__search({ query: "pi-go" });
const tel = await tools.deferred_metrics({ key: "srv-9" });
return await tools.combine_step({ doc: doc.structuredContent.hit, metric: tel });
`

	h.Faux.SetResponses([]providers.FauxResponseStep{
		// Turn 1: discover deferred_metrics via tool_search
		&providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
				providers.FauxToolCall(
					toolsearchext.ToolSearchToolName,
					jsonx.ObjectOf("query", "telemetry metrics"),
					&providers.FauxToolCallOptions{Id: tpbenchStrPtr("call_ts_1")},
				),
			}, &providers.FauxAssistantMessageOptions{StopReason: &stopTool}),
		},
		// Turn 2: confirm deferred_metrics became declared, then run multi-tool async codemode pipeline
		providers.FauxResponseFactory(func(_ context.Context, tr *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
			for _, tl := range ai.GetCurrentTools(tr.Messages) {
				if tl.Name == "deferred_metrics" {
					turn2DeclaredDeferred = true
				}
			}
			for _, m := range tr.Messages {
				if trMsg, ok := m.(*ai.ToolResultMessage); ok && trMsg.ToolCallId == "call_ts_1" {
					if len(trMsg.Content) > 0 {
						if tc, ok := trMsg.Content[0].(*ai.TextContent); ok {
							toolSearchOutput = tc.Text
						}
					}
				}
			}
			return providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
				providers.FauxToolCall(
					codemodeext.CodemodeToolName,
					jsonx.ObjectOf("code", pipelineScript),
					&providers.FauxToolCallOptions{Id: tpbenchStrPtr("call_cm_pipe")},
				),
			}, &providers.FauxAssistantMessageOptions{StopReason: &stopTool}), nil
		}),
		// Turn 3: check pipeline result, invoke codemode return null;
		providers.FauxResponseFactory(func(_ context.Context, tr *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
			for _, m := range tr.Messages {
				if trMsg, ok := m.(*ai.ToolResultMessage); ok && trMsg.ToolCallId == "call_cm_pipe" {
					var parts []string
					for _, blk := range trMsg.Content {
						if tc, ok := blk.(*ai.TextContent); ok {
							parts = append(parts, tc.Text)
						}
					}
					pipelineOutput = strings.Join(parts, "\n")
				}
			}
			return providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
				providers.FauxToolCall(
					codemodeext.CodemodeToolName,
					jsonx.ObjectOf("code", "return null;"),
					&providers.FauxToolCallOptions{Id: tpbenchStrPtr("call_cm_null")},
				),
			}, &providers.FauxAssistantMessageOptions{StopReason: &stopTool}), nil
		}),
		// Turn 4: verify return null; result and finish
		providers.FauxResponseFactory(func(_ context.Context, tr *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
			for _, m := range tr.Messages {
				if trMsg, ok := m.(*ai.ToolResultMessage); ok && trMsg.ToolCallId == "call_cm_null" {
					if trMsg.IsError {
						nullOutput = "ERROR:"
					}
					for _, blk := range trMsg.Content {
						if tc, ok := blk.(*ai.TextContent); ok {
							nullOutput += tc.Text + "|"
						}
					}
				}
			}
			return providers.FauxAssistantMessage(providers.FauxAssistantText("builtins-complete"), &providers.FauxAssistantMessageOptions{StopReason: &stopDone}), nil
		}),
	})

	if err := h.Session.Prompt(ctx, "Find deferred_metrics and run multi-tool pipeline"); err != nil {
		t.Fatalf("Prompt builtin pipeline: %v", err)
	}

	if !turn2DeclaredDeferred {
		t.Fatalf("expected tool_search to promote deferred_metrics into declared tools on Turn 2 (toolSearchOutput=%q)", toolSearchOutput)
	}
	if !strings.Contains(pipelineOutput, "combined:doc-hit:pi-go+metric:srv-9") {
		t.Fatalf("unexpected codemode multi-tool pipeline output: %q", pipelineOutput)
	}
	if strings.HasPrefix(nullOutput, "ERROR:") || !strings.Contains(nullOutput, "null") {
		t.Fatalf("expected codemode `return null;` to succeed emitting `null`, got %q", nullOutput)
	}
	mcpMu.Lock()
	recordedMcpCalls := append([]string(nil), mcpCalls...)
	mcpMu.Unlock()
	if !slices.Contains(recordedMcpCalls, "search:pi-go") {
		t.Fatalf("expected in-memory MCP server to receive search:pi-go, got %v", recordedMcpCalls)
	}
}

func BenchmarkPort_CodemodeQuickJS(b *testing.B) {
	ctx := context.Background()
	tmpDir := b.TempDir()
	echoExt := core.InlineExtension{
		Name: "echo-bench",
		Factory: func(pi core.ExtensionAPI) error {
			pi.RegisterTool(core.ToolDefinition{
				Name:        "echo_val",
				Label:       "Echo Value",
				Description: "Returns integer payload",
				Exposure:    core.ToolExposureCodemode,
				Parameters: tpbenchObjectSchema(struct {
					name   string
					schema *typebox.Schema
				}{"v", typebox.Integer()}),
				OutputSchema: tpbenchObjectSchema(struct {
					name   string
					schema *typebox.Schema
				}{"v", typebox.Integer()}),
				Execute: func(_ context.Context, _ string, input *jsonx.Object, _ agent.AgentToolUpdateCallback, _ core.ExtensionToolContext) (agent.AgentToolResult, error) {
					v, _ := input.GetNumber("v")
					return agent.AgentToolResult{
						Content:           ai.ContentList{&ai.TextContent{Type: "text", Text: fmt.Sprintf("%d", int(v))}},
						StructuredContent: jsonx.ObjectOf("v", float64(int(v))),
					}, nil
				},
			})
			return nil
		},
	}
	rl := core.NewDefaultResourceLoader(core.DefaultResourceLoaderOptions{
		Cwd:                       tmpDir,
		AgentDir:                  tmpDir,
		SettingsManager:           core.SettingsManagerInMemory(),
		NoSkills:                  true,
		NoPromptTemplates:         true,
		NoThemes:                  true,
		NoContextFiles:            true,
		DisabledBuiltinExtensions: []string{"mcp", "llama.cpp", "tool-search"},
		ExtensionFactories:        []core.InlineExtension{echoExt},
	})
	defer rl.Close()
	if err := rl.Reload(ctx); err != nil {
		b.Fatal(err)
	}
	h := tpbenchNewFauxSession(b, &core.CreateAgentSessionOptions{
		Cwd:            tmpDir,
		AgentDir:       tmpDir,
		ResourceLoader: rl,
		SessionManager: core.SessionManagerInMemory(tmpDir),
	})
	defer h.Close()

	h.Session.SetActiveToolsByName([]string{codemodeext.CodemodeToolName, "echo_val"})
	stopTool := ai.StopReasonToolUse
	scriptArgs := jsonx.ObjectOf("code", "const r = await tools.echo_val({ v: 42 }); return r.v + 1;")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.Faux.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
					providers.FauxToolCall(
						codemodeext.CodemodeToolName,
						scriptArgs,
						&providers.FauxToolCallOptions{Id: tpbenchStrPtr(fmt.Sprintf("call_cm_%d", i))},
					),
				}, &providers.FauxAssistantMessageOptions{StopReason: &stopTool}),
			},
			providers.FauxResponseFactory(func(_ context.Context, tr *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
				last := tr.Messages[len(tr.Messages)-1]
				if tm, ok := last.(*ai.ToolResultMessage); ok && tm.IsError {
					return nil, fmt.Errorf("codemode tool error in benchmark")
				}
				return providers.FauxAssistantMessage(providers.FauxAssistantText("ok"), nil), nil
			}),
		})
		if err := h.Session.Prompt(ctx, "bench", nil); err != nil {
			b.Fatalf("Prompt: %v", err)
		}
	}
	b.StopTimer()
}

// ============================================================================
// Subsystem 4: AgentSession Compaction, Tree Branch/Resume, RPC & Headless vt TUI
// ============================================================================

type tpbenchVtTerminal struct {
	mu            sync.Mutex
	emulator      *vt.Emulator
	closed        bool
	inputHandler  func(data string)
	resizeHandler func()
	columns       int
	rows          int
}

func tpbenchNewVtTerminal(columns, rows int) *tpbenchVtTerminal {
	emu := vt.NewEmulator(columns, rows)
	emu.RegisterApcHandler(func([]byte) bool { return true })
	go func() {
		_, _ = io.Copy(io.Discard, emu)
	}()
	return &tpbenchVtTerminal{
		emulator: emu,
		columns:  columns,
		rows:     rows,
	}
}

func (v *tpbenchVtTerminal) close() {
	v.mu.Lock()
	// Avoid v.emulator.Close() due to upstream data race against io.Copy drainer.
	v.closed = true
	v.inputHandler = nil
	v.resizeHandler = nil
	v.mu.Unlock()
}

func (v *tpbenchVtTerminal) Start(onInput func(data string), onResize func()) {
	v.mu.Lock()
	if !v.closed && v.emulator != nil {
		v.inputHandler = onInput
		v.resizeHandler = onResize
	}
	v.mu.Unlock()
}

func (v *tpbenchVtTerminal) DrainInput(*int64, *int64) error { return nil }
func (v *tpbenchVtTerminal) Stop() {
	v.mu.Lock()
	v.inputHandler = nil
	v.resizeHandler = nil
	v.mu.Unlock()
}
func (v *tpbenchVtTerminal) Write(data string) {
	v.mu.Lock()
	if !v.closed && v.emulator != nil {
		_, _ = v.emulator.WriteString(data)
	}
	v.mu.Unlock()
}
func (v *tpbenchVtTerminal) Columns() int              { return v.columns }
func (v *tpbenchVtTerminal) Rows() int                 { return v.rows }
func (v *tpbenchVtTerminal) KittyProtocolActive() bool { return false }
func (v *tpbenchVtTerminal) MoveBy(lines int) {
	if lines == 0 {
		return
	}
	v.mu.Lock()
	if !v.closed && v.emulator != nil {
		if lines > 0 {
			_, _ = v.emulator.WriteString(fmt.Sprintf("\x1b[%dB", lines))
		} else {
			_, _ = v.emulator.WriteString(fmt.Sprintf("\x1b[%dA", -lines))
		}
	}
	v.mu.Unlock()
}
func (v *tpbenchVtTerminal) HideCursor()                        { v.Write("\x1b[?25l") }
func (v *tpbenchVtTerminal) ShowCursor()                        { v.Write("\x1b[?25h") }
func (v *tpbenchVtTerminal) ClearLine()                         { v.Write("\x1b[K") }
func (v *tpbenchVtTerminal) ClearFromCursor()                   { v.Write("\x1b[J") }
func (v *tpbenchVtTerminal) ClearScreen()                       { v.Write("\x1b[2J\x1b[H") }
func (v *tpbenchVtTerminal) SetTitle(title string)              { v.Write("\x1b]0;" + title + "\x07") }
func (v *tpbenchVtTerminal) SetProgress(bool)                   {}
func (v *tpbenchVtTerminal) SetProgramStatus(tui.ProgramStatus) {}

func (v *tpbenchVtTerminal) viewportText() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	lines := make([]string, v.rows)
	for y := 0; y < v.rows; y++ {
		var b strings.Builder
		for x := 0; x < v.columns; x++ {
			c := v.emulator.CellAt(x, y)
			if c == nil || c.IsZero() {
				continue
			}
			if c.Content == "" {
				b.WriteByte(' ')
			} else {
				b.WriteString(c.Content)
			}
		}
		lines[y] = strings.TrimRight(b.String(), " ")
	}
	return strings.Join(lines, "\n")
}

func tpbenchCreateFauxRuntime(tb testing.TB, customTools ...*agent.AgentTool) (*core.AgentSessionRuntime, *providers.FauxProviderHandle, string, string) {
	tb.Helper()
	tb.Setenv("PI_OFFLINE", "1")
	ctx := context.Background()
	tmpDir := tb.TempDir()
	cwd := filepath.Join(tmpDir, "work")
	agentDir := filepath.Join(tmpDir, "agent")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		tb.Fatal(err)
	}
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		tb.Fatal(err)
	}

	modelsPath := filepath.Join(agentDir, "models.json")
	faux, err := providers.FauxProvider(&providers.RegisterFauxProviderOptions{
		Models: []providers.FauxModelDefinition{
			{
				Id:            "faux-stress-1",
				Name:          tpbenchStrPtr("Faux Stress 1"),
				ContextWindow: tpbenchIntPtr(64000),
				MaxTokens:     tpbenchIntPtr(4096),
				Input:         []string{"text"},
			},
		},
	})
	if err != nil {
		tb.Fatalf("FauxProvider: %v", err)
	}
	keepRecent := float64(60)
	reserve := float64(100)

	factory := func(rtCtx context.Context, args core.AgentSessionRuntimeCreateArgs) (*core.CreateAgentSessionRuntimeResult, error) {
		sm := core.SettingsManagerInMemory()
		sm.ApplyOverrides(&core.Settings{
			Compaction: &core.SettingsCompactionSettings{
				Enabled:          tpbenchBoolPtr(true),
				KeepRecentTokens: &keepRecent,
				ReserveTokens:    &reserve,
			},
		})
		rtModels, err := core.CreateModelRuntime(rtCtx, &core.CreateModelRuntimeOptions{
			Credentials:       ai.NewInMemoryCredentialStore(),
			ModelsStore:       core.NewInMemoryCodingAgentModelsStore(),
			ModelsPath:        &modelsPath,
			AllowModelNetwork: false,
		})
		if err != nil {
			return nil, err
		}
		if err := rtModels.RegisterNativeProvider(faux.Provider); err != nil {
			rtModels.Close()
			return nil, err
		}
		if _, err := rtModels.Refresh(rtCtx, &ai.ModelsRefreshOptions{AllowNetwork: tpbenchBoolPtr(false)}); err != nil {
			rtModels.Close()
			return nil, err
		}
		services, err := core.CreateAgentSessionServices(rtCtx, &core.CreateAgentSessionServicesOptions{
			Cwd:               args.Cwd,
			AgentDir:          args.AgentDir,
			SettingsManager:   sm,
			ModelRuntime:      rtModels,
			NoExtensions:      true,
			NoSkills:          true,
			NoPromptTemplates: true,
			NoThemes:          true,
			NoContextFiles:    true,
		})
		if err != nil {
			rtModels.Close()
			return nil, err
		}
		res, err := core.CreateAgentSessionFromServices(rtCtx, core.CreateAgentSessionFromServicesOptions{
			Services:          services,
			SessionManager:    args.SessionManager,
			SessionStartEvent: args.SessionStartEvent,
			Model:             faux.Models[0],
			Tools:             customTools,
		})
		if err != nil {
			rtModels.Close()
			return nil, err
		}
		return &core.CreateAgentSessionRuntimeResult{
			CreateAgentSessionResult: *res,
			Services:                 services,
		}, nil
	}

	smgr, err := core.SessionManagerCreate(cwd, core.GetDefaultSessionDir(cwd, agentDir))
	if err != nil {
		tb.Fatal(err)
	}
	rt, err := core.CreateAgentSessionRuntime(ctx, factory, core.CreateAgentSessionRuntimeOptions{
		Cwd:            cwd,
		AgentDir:       agentDir,
		SessionManager: smgr,
	})
	if err != nil {
		tb.Fatalf("CreateAgentSessionRuntime: %v", err)
	}
	return rt, faux, cwd, agentDir
}

func TestPortConformance_SessionRpcAndVtTui(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var noteMu sync.Mutex
	var recordedNotes []string
	noteTool := &agent.AgentTool{
		Name:        "note_store",
		Label:       "Note Store",
		Description: "Records deterministic notes",
		Parameters: tpbenchObjectSchema(struct {
			name   string
			schema *typebox.Schema
		}{"note", typebox.String()}),
		Execute: func(_ context.Context, _ string, rawParams any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
			note := "empty"
			if obj, ok := rawParams.(*jsonx.Object); ok {
				if v, ok := obj.GetString("note"); ok {
					note = v
				}
			}
			noteMu.Lock()
			recordedNotes = append(recordedNotes, note)
			noteMu.Unlock()
			return agent.AgentToolResult{
				Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "stored:" + note}},
			}, nil
		},
	}

	// 1. Multi-turn tool calls, compaction, Session Tree branch, and file resume reload.
	rt, faux, cwd, agentDir := tpbenchCreateFauxRuntime(t, noteTool)
	defer func() {
		_ = rt.Dispose(context.Background())
	}()

	stopTool := ai.StopReasonToolUse
	stopDone := ai.StopReasonStop
	longPad := strings.Repeat("telemetry-context-segment ", 45)
	faux.SetResponses([]providers.FauxResponseStep{
		// Turn 1: tool call + final text
		&providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
				providers.FauxToolCall("note_store", jsonx.ObjectOf("note", "turn1"), &providers.FauxToolCallOptions{Id: tpbenchStrPtr("tc_1")}),
			}, &providers.FauxAssistantMessageOptions{StopReason: &stopTool}),
		},
		&providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(providers.FauxAssistantText("Turn 1 acknowledged "+longPad), &providers.FauxAssistantMessageOptions{StopReason: &stopDone}),
		},
		// Turn 2: tool call + long reply
		&providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
				providers.FauxToolCall("note_store", jsonx.ObjectOf("note", "turn2"), &providers.FauxToolCallOptions{Id: tpbenchStrPtr("tc_2")}),
			}, &providers.FauxAssistantMessageOptions{StopReason: &stopTool}),
		},
		&providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(providers.FauxAssistantText("Turn 2 acknowledged "+longPad), &providers.FauxAssistantMessageOptions{StopReason: &stopDone}),
		},
	})

	if err := rt.Session.Prompt(ctx, "First turn "+longPad); err != nil {
		t.Fatalf("Turn 1 Prompt: %v", err)
	}
	branchLeaf := rt.Session.SessionManager.GetLeafId()
	if branchLeaf == nil {
		t.Fatal("expected non-nil leaf after Turn 1")
	}
	if err := rt.Session.Prompt(ctx, "Second turn "+longPad); err != nil {
		t.Fatalf("Turn 2 Prompt: %v", err)
	}
	faux.SetResponses([]providers.FauxResponseStep{
		&providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(providers.FauxAssistantText("## Summary\nTurn 1 and Turn 2 compacted cleanly."), &providers.FauxAssistantMessageOptions{StopReason: &stopDone}),
		},
		&providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(providers.FauxAssistantText("## Turn Prefix Summary\nSplit turn compacted cleanly."), &providers.FauxAssistantMessageOptions{StopReason: &stopDone}),
		},
	})
	compRes, err := rt.Session.Compact(ctx)
	if err != nil {
		t.Fatalf("AgentSession.Compact: %v", err)
	}
	if !strings.Contains(compRes.Summary, "compacted cleanly") || compRes.TokensBefore <= 0 {
		t.Fatalf("unexpected CompactionResult: %+v", compRes)
	}

	// Branch back to Turn 1 leaf using NavigateTree, then add Turn 3 and verify branch resume from disk.
	navRes, err := rt.Session.NavigateTree(ctx, *branchLeaf)
	if err != nil || navRes.Cancelled {
		t.Fatalf("NavigateTree(%s): res=%+v err=%v", *branchLeaf, navRes, err)
	}
	faux.SetResponses([]providers.FauxResponseStep{
		&providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(providers.FauxAssistantText("Branched turn 3 reply"), &providers.FauxAssistantMessageOptions{StopReason: &stopDone}),
		},
	})
	if err := rt.Session.Prompt(ctx, "Branched Turn 3 prompt"); err != nil {
		t.Fatalf("Branch Turn 3 Prompt: %v", err)
	}
	sessionFilePtr := rt.Session.SessionFile()
	if sessionFilePtr == nil || *sessionFilePtr == "" {
		t.Fatalf("expected persisted session file path")
	}
	sessionFile := *sessionFilePtr
	expectedBranchLeaf := rt.Session.SessionManager.GetLeafId()
	_ = expectedBranchLeaf

	reopenedSM, err := core.SessionManagerOpen(sessionFile)
	if err != nil {
		t.Fatalf("SessionManagerOpen(%s): %v", sessionFile, err)
	}
	resumed, err := core.CreateAgentSession(ctx, &core.CreateAgentSessionOptions{
		Cwd:               cwd,
		AgentDir:          agentDir,
		ModelRuntime:      rt.Services.ModelRuntime,
		Model:             faux.Models[0],
		SessionManager:    reopenedSM,
		NoExtensions:      true,
		NoSkills:          true,
		NoPromptTemplates: true,
		NoThemes:          true,
		NoContextFiles:    true,
	})
	if err != nil {
		t.Fatalf("resume CreateAgentSession: %v", err)
	}
	if err := resumed.Session.Reload(ctx); err != nil {
		resumed.Session.Dispose()
		t.Fatalf("resumed Reload: %v", err)
	}
	var resumedTxt strings.Builder
	for _, m := range resumed.Session.Messages() {
		if am, ok := m.(*ai.AssistantMessage); ok {
			for _, b := range am.Content {
				if tc, ok := b.(*ai.TextContent); ok {
					resumedTxt.WriteString(tc.Text)
				}
			}
		}
	}
	resumed.Session.Dispose()
	if !strings.Contains(resumedTxt.String(), "Branched turn 3 reply") {
		t.Fatalf("expected resumed branch transcript to include 'Branched turn 3 reply', got %q", resumedTxt.String())
	}

	noteMu.Lock()
	notesSnap := append([]string(nil), recordedNotes...)
	noteMu.Unlock()
	if !slices.Equal(notesSnap, []string{"turn1", "turn2"}) {
		t.Fatalf("note_store recorded calls = %v, want [turn1 turn2]", notesSnap)
	}

	// 2. JSONL RPC command roundtrip via rpc.RunRpcMode over io.Pipe().
	rpcRt, rpcFaux, _, _ := tpbenchCreateFauxRuntime(t)
	defer func() {
		_ = rpcRt.Dispose(context.Background())
	}()
	rpcFaux.SetResponses([]providers.FauxResponseStep{
		&providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(providers.FauxAssistantText("RPC_ROUNDTRIP_OK"), &providers.FauxAssistantMessageOptions{StopReason: &stopDone}),
		},
	})
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	rpcDone := make(chan error, 1)
	go func() {
		defer func() { _ = outW.Close() }()
		rpcDone <- rpc.RunRpcMode(ctx, rpcRt, &rpc.RpcModeOptions{
			Stdin:      inR,
			Stdout:     outW,
			Stderr:     io.Discard,
			SignalChan: make(chan os.Signal, 1),
			OnExit:     func(int) {},
		})
	}()

	rpcLines := make(chan string, 64)
	scanErrCh := make(chan error, 1)
	go func() {
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			rpcLines <- sc.Text()
		}
		scanErrCh <- sc.Err()
		close(rpcLines)
	}()

	waitRpcResponse := func(reqID string) *jsonx.Object {
		deadline := time.After(5 * time.Second)
		for {
			select {
			case line, ok := <-rpcLines:
				if !ok {
					t.Fatalf("RPC stdout closed waiting for response %s", reqID)
				}
				v, err := jsonx.Parse([]byte(line))
				if err != nil {
					continue
				}
				if obj, ok := v.(*jsonx.Object); ok {
					typ, _ := obj.GetString("type")
					id, _ := obj.GetString("id")
					if typ == "response" && id == reqID {
						return obj
					}
				}
			case <-deadline:
				t.Fatalf("timed out waiting for RPC response %s", reqID)
			}
		}
	}

	if _, err := io.WriteString(inW, `{"id":"rpc-1","type":"get_state"}`+"\n"); err != nil {
		t.Fatalf("write RPC get_state: %v", err)
	}
	resp1 := waitRpcResponse("rpc-1")
	if okVal, _ := resp1.GetBool("success"); !okVal {
		t.Fatalf("RPC get_state failed: %v", resp1)
	}

	if _, err := io.WriteString(inW, `{"id":"rpc-2","type":"prompt","message":"Say RPC_ROUNDTRIP_OK"}`+"\n"); err != nil {
		t.Fatalf("write RPC prompt: %v", err)
	}
	resp2 := waitRpcResponse("rpc-2")
	if okVal, _ := resp2.GetBool("success"); !okVal {
		t.Fatalf("RPC prompt failed: %v", resp2)
	}
	// Wait until streaming settles before requesting last assistant text.
	tpbenchWaitFor(t, 5*time.Second, func() bool { return !rpcRt.Session.IsStreaming() })
	if _, err := io.WriteString(inW, `{"id":"rpc-3","type":"get_last_assistant_text"}`+"\n"); err != nil {
		t.Fatalf("write RPC get_last_assistant_text: %v", err)
	}
	resp3 := waitRpcResponse("rpc-3")
	dataObj, _ := resp3.GetObject("data")
	txt, _ := dataObj.GetString("text")
	if txt != "RPC_ROUNDTRIP_OK" {
		t.Fatalf("RPC get_last_assistant_text = %q (resp=%v), want RPC_ROUNDTRIP_OK", txt, resp3)
	}
	_ = inW.Close()
	if err := <-rpcDone; err != nil {
		t.Fatalf("RunRpcMode exit error: %v", err)
	}
	if err := <-scanErrCh; err != nil {
		t.Fatalf("RPC stdout scanner error: %v", err)
	}

	// 3. Headless vt TUI terminal frame rendering via InteractiveMode.
	if _, err := theme.InitTheme("dark", false); err != nil {
		t.Fatalf("InitTheme: %v", err)
	}
	t.Cleanup(func() {
		_, _ = theme.InitTheme("dark", false)
	})
	vtRt, vtFaux, _, _ := tpbenchCreateFauxRuntime(t)
	defer func() {
		_ = vtRt.Dispose(context.Background())
	}()
	vtFaux.SetResponses([]providers.FauxResponseStep{
		&providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(providers.FauxAssistantText("VT_FRAME_RENDER_OK"), &providers.FauxAssistantMessageOptions{StopReason: &stopDone}),
		},
	})
	vtTerm := tpbenchNewVtTerminal(100, 28)
	defer vtTerm.close()

	mode := interactive.NewInteractiveMode(vtRt, &interactive.InteractiveModeOptions{
		Terminal:              vtTerm,
		InitialMessage:        tpbenchStrPtr("Trigger VT frame"),
		DisableVersionCheck:   true,
		DisableTmuxCheck:      true,
		IsInteractiveTerminal: func() bool { return true },
	})
	vtCtx, vtCancel := context.WithCancel(ctx)
	vtDone := make(chan error, 1)
	go func() {
		vtDone <- mode.Run(vtCtx)
	}()

	tpbenchWaitFor(t, 5*time.Second, func() bool {
		frame := vtTerm.viewportText()
		return strings.Contains(frame, "VT_FRAME_RENDER_OK")
	})
	vtCancel()
	select {
	case err := <-vtDone:
		if err != nil {
			t.Fatalf("InteractiveMode.Run error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for InteractiveMode.Run shutdown")
	}
}

func BenchmarkPort_VtTuiRender(b *testing.B) {
	vtTerm := tpbenchNewVtTerminal(100, 30)
	defer vtTerm.close()
	ui := tui.NewTuiMainScreen(vtTerm, nil, nil)
	header := tui.NewText("pi-go headless vt TUI frame benchmark", 1, 0, nil)
	body := tui.NewBox(1, 0, nil)
	body.AddChild(tui.NewText("Streaming assistant output across ANSI wrap boundaries.", 0, 0, nil))
	ui.AddChild(header)
	ui.AddChild(body)

	force := true
	ui.Start()
	defer ui.Stop(nil)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if i&1 == 0 {
			header.SetText("pi-go frame A")
		} else {
			header.SetText("pi-go frame B")
		}
		ui.RenderNow(&force)
	}
}

// =============================================================================
// Subsystem 5: Distributed stack (protocol/chord, client/server Unix RPC, durable sqlite & jsonl)
// =============================================================================

const tpbenchRpcServerId = protocol.ServerId("00000000-0000-4000-8000-000000000001")

type tpbenchRpcAttachment struct {
	endpoint chord.RemoteServiceEndpoint
}

func (a *tpbenchRpcAttachment) InvokeService(call chord.ServiceCall, publish chord.ServiceUpdatePublisher, callContext chord.Context) (chord.JsonValue, bool, error) {
	return a.endpoint.Invoke(call, publish, callContext)
}

func (a *tpbenchRpcAttachment) Release(_ chord.Context) error {
	a.endpoint.Dispose()
	return nil
}

type tpbenchRpcSession struct {
	provider      *chord.RemoteServiceProvider
	terminatedCh  chan error
	terminateOnce sync.Once
}

func (s *tpbenchRpcSession) AttachClient(_ chord.Context) (server.RoutedSessionAttachment, error) {
	return &tpbenchRpcAttachment{
		endpoint: chord.CreateRemoteServiceEndpoint(s.provider),
	}, nil
}

func (s *tpbenchRpcSession) Terminated() <-chan error { return s.terminatedCh }

func (s *tpbenchRpcSession) Close(_ chord.Context) error {
	s.terminateOnce.Do(func() {
		s.terminatedCh <- nil
	})
	return s.provider.Dispose()
}

type tpbenchRpcHost struct {
	services  server.RoutedServerServiceHost
	sessionId string
	session   *tpbenchRpcSession
}

func (h *tpbenchRpcHost) ServerServices() server.RoutedServerServiceHost { return h.services }

func (h *tpbenchRpcHost) ResolveSession(sessionId string, _ chord.Context) (server.SessionMetadata, error) {
	if sessionId != h.sessionId {
		return nil, server.NewSessionNotFoundError("Unknown session: " + sessionId)
	}
	return server.BasicSessionMetadata{Id: sessionId}, nil
}

func (h *tpbenchRpcHost) OpenSession(meta server.SessionMetadata, _ chord.Context) (server.RoutedSessionHandle, error) {
	if meta == nil || meta.SessionId() != h.sessionId {
		return nil, server.NewSessionNotFoundError("Unknown session")
	}
	return h.session, nil
}

type tpbenchStepInput struct {
	Label string `json:"label"`
	Value int    `json:"value"`
}

type tpbenchStepCheckpoint struct {
	Phase string `json:"phase"`
	Key   string `json:"key,omitzero"`
}

type tpbenchStepResult struct {
	Receipt int    `json:"receipt"`
	Label   string `json:"label"`
}

func tpbenchVerifyDurableBackend(t *testing.T, backend string, openStorage func() (durable.Storage, error)) {
	t.Helper()
	ctx := chord.BackgroundContext

	noteDoc, err := durable.DefineDoc(durable.DocDefinition{
		Kind:    "bench.note." + backend,
		Version: 1,
		Scope:   durable.DocumentScopeConversation,
		History: durable.DocumentHistoryRewindable,
		Initial: func() durable.JsonObject {
			return jsonx.ObjectOf("title", "", "tags", []any{})
		},
	})
	if err != nil {
		t.Fatalf("DefineDoc(%s): %v", backend, err)
	}

	var appliedMu sync.Mutex
	appliedKeys := make(map[string]int)
	var applyCalls atomic.Int32
	var allowFinish atomic.Bool

	applyEntered := make(chan struct{})
	var applyEnteredOnce sync.Once

	stepTask := durable.DefineTask(durable.TaskDefinition[tpbenchStepInput, tpbenchStepCheckpoint, tpbenchStepResult, struct{}]{
		Name:    "bench.step",
		Version: 1,
		Initial: func(_ tpbenchStepInput) tpbenchStepCheckpoint {
			return tpbenchStepCheckpoint{Phase: "prepare"}
		},
		Phases: map[string]durable.PhaseHandler[tpbenchStepInput, tpbenchStepCheckpoint, tpbenchStepResult, struct{}]{
			"prepare": func(ctx chord.Context, task durable.RunningTask[tpbenchStepInput, tpbenchStepCheckpoint], rt *durable.TaskRuntime[tpbenchStepInput, tpbenchStepCheckpoint, tpbenchStepResult, struct{}]) error {
				if _, err := rt.MemoPut(ctx, "step-key", fmt.Sprintf("%s:%d", task.Input.Label, task.Input.Value)); err != nil {
					return err
				}
				return rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[tpbenchStepInput, tpbenchStepCheckpoint]) (*durable.NextTaskState[tpbenchStepCheckpoint, tpbenchStepResult], error) {
					return &durable.NextTaskState[tpbenchStepCheckpoint, tpbenchStepResult]{
						Status:     durable.TaskStateRunning,
						Checkpoint: tpbenchStepCheckpoint{Phase: "apply", Key: fmt.Sprintf("key-%d", task.Id)},
					}, nil
				})
			},
			"apply": func(ctx chord.Context, task durable.RunningTask[tpbenchStepInput, tpbenchStepCheckpoint], rt *durable.TaskRuntime[tpbenchStepInput, tpbenchStepCheckpoint, tpbenchStepResult, struct{}]) error {
				applyCalls.Add(1)
				appliedMu.Lock()
				receipt, ok := appliedKeys[task.Checkpoint.Key]
				if !ok {
					receipt = task.Input.Value * 10
					appliedKeys[task.Checkpoint.Key] = receipt
				}
				appliedMu.Unlock()

				if !allowFinish.Load() {
					applyEnteredOnce.Do(func() { close(applyEntered) })
					if sig := rt.Signal(); sig != nil {
						<-sig.Done()
					}
					return nil
				}

				return rt.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[tpbenchStepInput, tpbenchStepCheckpoint]) (*durable.NextTaskState[tpbenchStepCheckpoint, tpbenchStepResult], error) {
					return &durable.NextTaskState[tpbenchStepCheckpoint, tpbenchStepResult]{
						Status:     durable.TaskStateTerminal,
						Checkpoint: task.Checkpoint,
						Outcome: &durable.TypedTaskOutcome[tpbenchStepResult]{
							Status: durable.TaskOutcomeCompleted,
							Result: tpbenchStepResult{Receipt: receipt, Label: task.Input.Label},
							HasRes: true,
						},
					}, nil
				})
			},
		},
		Abort: func(ctx chord.Context, current durable.RunningTask[tpbenchStepInput, tpbenchStepCheckpoint], rt *durable.TaskRuntime[tpbenchStepInput, tpbenchStepCheckpoint, tpbenchStepResult, struct{}]) error {
			return rt.Commit(ctx, func(_ durable.Tx, cur durable.RunningTask[tpbenchStepInput, tpbenchStepCheckpoint]) (*durable.NextTaskState[tpbenchStepCheckpoint, tpbenchStepResult], error) {
				return &durable.NextTaskState[tpbenchStepCheckpoint, tpbenchStepResult]{
					Status:     durable.TaskStateTerminal,
					Checkpoint: cur.Checkpoint,
					Outcome:    &durable.TypedTaskOutcome[tpbenchStepResult]{Status: durable.TaskOutcomeAborted},
				}, nil
			})
		},
	})

	newRegistry := func() durable.Registry {
		reg := durable.CreateRegistry()
		if err := reg.Install(durable.DefineExtension(&durable.Extension{
			Name:  "bench-ext",
			Tasks: []durable.AnyTask{stepTask},
		})); err != nil {
			t.Fatalf("Install extension (%s): %v", backend, err)
		}
		return reg
	}

	// Stage 1: Persist conversation config, entries, document, and interrupt mid-task after prepare checkpoint.
	st1, err := openStorage()
	if err != nil {
		t.Fatalf("openStorage 1 (%s): %v", backend, err)
	}
	h1, err := durable.HarnessOpen(st1, durable.HarnessOptions{Registry: newRegistry()}, ctx)
	if err != nil {
		t.Fatalf("HarnessOpen 1 (%s): %v", backend, err)
	}
	root1, err := h1.Root(ctx)
	if err != nil {
		t.Fatalf("Root 1 (%s): %v", backend, err)
	}
	if err := root1.Configure(durable.AgentChange{
		Model: jsonx.Some(durable.ModelRef{Provider: "faux", ModelId: "bench-durable"}),
		Cwd:   jsonx.Some("/bench/" + backend),
	}, ctx); err != nil {
		t.Fatalf("Configure (%s): %v", backend, err)
	}
	msg1 := "durable entry one"
	msg2 := "durable entry two"
	rawTaskId, err := root1.Commit(ctx, func(tx durable.Tx) (any, error) {
		if _, err := tx.AppendEntry(root1.Id(), durable.EntryDraft{
			Kind: "message",
			Model: ai.MessageList{&ai.UserMessage{
				Role: "user", Content: ai.UserMessageContent{Text: &msg1}, Timestamp: 1001,
			}},
		}); err != nil {
			return nil, err
		}
		if _, err := tx.AppendEntry(root1.Id(), durable.EntryDraft{
			Kind: "message",
			Model: ai.MessageList{&ai.UserMessage{
				Role: "user", Content: ai.UserMessageContent{Text: &msg2}, Timestamp: 1002,
			}},
		}); err != nil {
			return nil, err
		}
		draft, err := tx.Doc(noteDoc, root1.Id())
		if err != nil {
			return nil, err
		}
		draft.Value.Set("title", "checkpointed-note")
		draft.Value.Set("tags", []any{backend, "crash-safe"})
		return tx.CreateTask(stepTask, tpbenchStepInput{Label: backend, Value: 9}, durable.TaskOptions{
			Ownership: durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
		})
	})
	if err != nil {
		t.Fatalf("Commit stage 1 (%s): %v", backend, err)
	}
	taskId := rawTaskId.(durable.TaskId)

	if err := h1.Resume(); err != nil {
		t.Fatalf("Resume 1 (%s): %v", backend, err)
	}
	select {
	case <-applyEntered:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for apply phase (%s)", backend)
	}
	if err := h1.Close(ctx); err != nil {
		t.Fatalf("Close 1 (%s): %v", backend, err)
	}

	// Stage 2: Reopen from disk, verify reconciled pending state at phase "apply", allow completion & resume.
	st2, err := openStorage()
	if err != nil {
		t.Fatalf("openStorage 2 (%s): %v", backend, err)
	}
	h2, err := durable.HarnessOpen(st2, durable.HarnessOptions{Registry: newRegistry()}, ctx)
	if err != nil {
		t.Fatalf("HarnessOpen 2 (%s): %v", backend, err)
	}
	rec, err := h2.GetTask(ctx, taskId)
	if err != nil || rec == nil {
		t.Fatalf("GetTask 2 (%s): rec=%v err=%v", backend, rec, err)
	}
	var cp tpbenchStepCheckpoint
	if err := jsonx.Decode(rec.State.Checkpoint, &cp); err != nil {
		t.Fatalf("decode checkpoint (%s): %v", backend, err)
	}
	memoVal, hasMemo := rec.Memos.Get("step-key")
	if !hasMemo || memoVal != fmt.Sprintf("%s:9", backend) || rec.State.Status != durable.TaskStatePending || cp.Phase != "apply" {
		t.Fatalf("unexpected crash-recovered task (%s): status=%s cp=%+v memo=%v", backend, rec.State.Status, cp, memoVal)
	}

	root2, err := h2.Root(ctx)
	if err != nil {
		t.Fatalf("Root 2 (%s): %v", backend, err)
	}
	agent2, err := root2.Agent(ctx)
	if err != nil {
		t.Fatalf("Agent 2 (%s): %v", backend, err)
	}
	if agent2.Model == nil || agent2.Model.ModelId != "bench-durable" || agent2.Cwd == nil || *agent2.Cwd != "/bench/"+backend {
		t.Fatalf("recovered agent mismatch (%s): %+v", backend, agent2)
	}
	noteSnap, err := h2.Snapshot(ctx, noteDoc, root2.Id())
	if err != nil || noteSnap == nil {
		t.Fatalf("recovered note doc (%s): snap=%v err=%v", backend, noteSnap, err)
	}
	if title, _ := noteSnap.GetString("title"); title != "checkpointed-note" {
		t.Fatalf("recovered note title (%s) = %q, want checkpointed-note", backend, title)
	}
	page, err := root2.Entries(durable.ConversationEntryQuery{}, 10, nil, ctx)
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("recovered entries (%s): len=%d err=%v", backend, len(page.Items), err)
	}

	allowFinish.Store(true)
	if err := h2.Resume(); err != nil {
		t.Fatalf("Resume 2 (%s): %v", backend, err)
	}
	settled, err := h2.WaitForTask(ctx, taskId)
	if err != nil || settled == nil {
		t.Fatalf("WaitForTask 2 (%s): settled=%v err=%v", backend, settled, err)
	}
	rawRes, hasRes := settled.State.Outcome.Result.Get()
	if !hasRes || settled.State.Outcome.Status != durable.TaskOutcomeCompleted {
		t.Fatalf("settled outcome (%s) = %+v", backend, settled.State.Outcome)
	}
	var res tpbenchStepResult
	if err := jsonx.Decode(rawRes, &res); err != nil || res.Receipt != 90 || res.Label != backend {
		t.Fatalf("settled result (%s) = %+v err=%v", backend, res, err)
	}
	if calls := applyCalls.Load(); calls != 2 {
		t.Fatalf("expected 2 apply executions across crash/resume (%s), got %d", backend, calls)
	}
	if err := h2.Close(ctx); err != nil {
		t.Fatalf("Close 2 (%s): %v", backend, err)
	}

	// Stage 3: Final reopen verifies durable receipt survives full process shutdown cleanly.
	st3, err := openStorage()
	if err != nil {
		t.Fatalf("openStorage 3 (%s): %v", backend, err)
	}
	h3, err := durable.HarnessOpen(st3, durable.HarnessOptions{Registry: newRegistry()}, ctx)
	if err != nil {
		t.Fatalf("HarnessOpen 3 (%s): %v", backend, err)
	}
	defer func() { _ = h3.Close(ctx) }()
	finalRec, err := h3.GetTask(ctx, taskId)
	if err != nil || finalRec == nil {
		t.Fatalf("GetTask 3 (%s): rec=%v err=%v", backend, finalRec, err)
	}
	gotJSON, _ := jsonx.Stringify(finalRec)
	wantJSON, _ := jsonx.Stringify(settled)
	if gotJSON != wantJSON {
		t.Fatalf("reopened receipt mismatch (%s):\n got: %s\nwant: %s", backend, gotJSON, wantJSON)
	}
}

func TestPortConformance_DistributedStack(t *testing.T) {
	t.Run("protocol_and_chord_state_delta", func(t *testing.T) {
		helloBytes, err := protocol.EncodeClientMessage(&protocol.ClientHello{
			Type:    "hello",
			Version: protocol.ProtocolVersion,
		}, nil)
		if err != nil {
			t.Fatalf("EncodeClientMessage(hello): %v", err)
		}
		reqBytes, err := protocol.EncodeClientMessage(&protocol.RequestEnvelope{
			Type: "request",
			Id:   "req-1",
			Target: &protocol.SessionTarget{
				ServerId:     tpbenchRpcServerId,
				SessionId:    "sess-1",
				AttachmentId: "att-1",
			},
			Call: jsonx.ObjectOf(
				"serviceId", "bench.counter",
				"member", "increment",
				"args", []any{float64(3)},
			),
		}, nil)
		if err != nil {
			t.Fatalf("EncodeClientMessage(request): %v", err)
		}

		combined := append(slices.Clone(helloBytes), reqBytes...)
		cDec, err := protocol.NewClientMessageDecoder(nil)
		if err != nil {
			t.Fatalf("NewClientMessageDecoder: %v", err)
		}
		var clientMsgs []protocol.ClientMessage
		for offset := 0; offset < len(combined); {
			step := 3
			if offset+step > len(combined) {
				step = len(combined) - offset
			}
			msgs, err := cDec.Push(combined[offset : offset+step])
			if err != nil {
				t.Fatalf("ClientMessageDecoder.Push: %v", err)
			}
			clientMsgs = append(clientMsgs, msgs...)
			offset += step
		}
		if err := cDec.End(); err != nil {
			t.Fatalf("ClientMessageDecoder.End: %v", err)
		}
		if len(clientMsgs) != 2 {
			t.Fatalf("expected 2 client messages, got %d", len(clientMsgs))
		}
		if h, ok := clientMsgs[0].(*protocol.ClientHello); !ok || h.Version != protocol.ProtocolVersion {
			t.Fatalf("unexpected decoded ClientHello: %#v", clientMsgs[0])
		}
		if r, ok := clientMsgs[1].(*protocol.RequestEnvelope); !ok || r.Id != "req-1" {
			t.Fatalf("unexpected decoded RequestEnvelope: %#v", clientMsgs[1])
		} else {
			parsedCall, err := chord.ParseServiceCall(r.Call)
			if err != nil || parsedCall.ServiceId != "bench.counter" || parsedCall.Member != "increment" {
				t.Fatalf("ParseServiceCall failed: call=%+v err=%v", parsedCall, err)
			}
		}

		srvBytes, err := protocol.EncodeServerMessage(&protocol.ResponseSuccess{
			Type:   "response",
			Id:     "req-1",
			Ok:     true,
			Result: jsonx.Some(chord.JsonValue(jsonx.ObjectOf("count", float64(3)))),
		}, nil)
		if err != nil {
			t.Fatalf("EncodeServerMessage: %v", err)
		}
		sDec, err := protocol.NewServerMessageDecoder(nil)
		if err != nil {
			t.Fatalf("NewServerMessageDecoder: %v", err)
		}
		srvMsgs, err := sDec.Push(srvBytes)
		if err != nil || len(srvMsgs) != 1 {
			t.Fatalf("ServerMessageDecoder.Push: msgs=%d err=%v", len(srvMsgs), err)
		}
		if err := sDec.End(); err != nil {
			t.Fatalf("ServerMessageDecoder.End: %v", err)
		}
		if rs, ok := srvMsgs[0].(*protocol.ResponseSuccess); !ok || rs.Id != "req-1" || !rs.Ok {
			t.Fatalf("unexpected decoded ResponseSuccess: %#v", srvMsgs[0])
		}

		// Verify Chord structural diff + immutable patch application.
		rev0 := jsonx.ObjectOf(
			"count", float64(0),
			"items", []any{
				jsonx.ObjectOf("id", "a", "done", false),
				jsonx.ObjectOf("id", "b", "done", false),
			},
			"meta", jsonx.ObjectOf("step", float64(1)),
		)
		rev1 := jsonx.ObjectOf(
			"count", float64(3),
			"items", []any{
				jsonx.ObjectOf("id", "a", "done", false),
				jsonx.ObjectOf("id", "b", "done", true),
				jsonx.ObjectOf("id", "c", "done", false),
			},
			"meta", jsonx.ObjectOf("step", float64(1)),
		)
		ops := chord.DiffRevisions(rev0, rev1)
		if len(ops) == 0 {
			t.Fatalf("expected non-empty DiffRevisions ops")
		}
		patched, err := chord.ApplyImmutableTrusted[chord.JsonValue](rev0, ops)
		if err != nil {
			t.Fatalf("ApplyImmutableTrusted: %v", err)
		}
		gotJSON, _ := jsonx.Stringify(patched)
		wantJSON, _ := jsonx.Stringify(rev1)
		if gotJSON != wantJSON {
			t.Fatalf("patched state mismatch:\n got: %s\nwant: %s", gotJSON, wantJSON)
		}
		// Original rev0 must remain unmodified.
		if cntAny, _ := rev0.Get("count"); cntAny != float64(0) {
			t.Fatalf("ApplyImmutableTrusted mutated rev0: count=%v", cntAny)
		}
	})

	t.Run("client_server_unix_socket_rpc_and_subscription", func(t *testing.T) {
		tmpSockDir, err := os.MkdirTemp("/tmp", "peb-")
		if err != nil {
			t.Fatalf("os.MkdirTemp(/tmp, peb-): %v", err)
		}
		defer func() { _ = os.RemoveAll(tmpSockDir) }()
		sockPath := filepath.Join(tmpSockDir, "s.sock")

		counterSvc, err := chord.DefineService[any]("bench.counter", nil)
		if err != nil {
			t.Fatalf("DefineService: %v", err)
		}
		state, err := chord.NewReplicatedState[chord.JsonValue](jsonx.ObjectOf("count", float64(0), "log", []any{}))
		if err != nil {
			t.Fatalf("NewReplicatedState: %v", err)
		}
		provider, err := chord.NewRemoteServiceProvider([]chord.RemoteServiceProviderEntry{{Id: counterSvc.Id}})
		if err != nil {
			t.Fatalf("NewRemoteServiceProvider: %v", err)
		}

		var counterMu sync.Mutex
		var counterVal float64
		var counterLog []any
		if err := provider.Provide(counterSvc, jsonx.ObjectOf(
			"state", state,
			"increment", func(delta float64) (float64, error) {
				counterMu.Lock()
				counterVal += delta
				next := counterVal
				counterLog = append(counterLog, fmt.Sprintf("+%g", delta))
				logCopy := slices.Clone(counterLog)
				counterMu.Unlock()

				if err := state.Replace(chord.BackgroundContext, jsonx.ObjectOf(
					"count", next,
					"log", logCopy,
				)); err != nil {
					return 0, err
				}
				return next, nil
			},
		)); err != nil {
			t.Fatalf("provider.Provide: %v", err)
		}

		host := &tpbenchRpcHost{
			services:  servertest.CreateTestServerServices(),
			sessionId: "sess-1",
			session: &tpbenchRpcSession{
				provider:     provider,
				terminatedCh: make(chan error, 1),
			},
		}
		listener, err := unix.CreateUnixListener(unix.UnixListenerOptions{Path: sockPath})
		if err != nil {
			t.Fatalf("CreateUnixListener: %v", err)
		}
		testSrv, err := servertest.CreateTestServer(servertest.TestServerOptions{
			Listeners: []server.ServerListener{listener},
			Host:      host,
			ServerId:  tpbenchRpcServerId,
			OnError: func(err error) {
				t.Logf("server OnError: %v", err)
			},
		})
		if err != nil {
			t.Fatalf("CreateTestServer: %v", err)
		}
		if err := testSrv.Server.Start(); err != nil {
			t.Fatalf("Server.Start: %v", err)
		}
		defer func() { _ = testSrv.Server.Close() }()

		factory, err := client.CreateUnixTransportFactory(client.UnixTransportOptions{Path: sockPath})
		if err != nil {
			t.Fatalf("CreateUnixTransportFactory: %v", err)
		}
		c, err := client.Connect(client.ClientOptions{
			ServerId:         tpbenchRpcServerId,
			TransportFactory: factory,
		})
		if err != nil {
			t.Fatalf("client.Connect: %v", err)
		}
		defer func() { _ = c.Dispose() }()

		if !c.Connected() || c.Hello() == nil || c.Hello().ServerId != tpbenchRpcServerId {
			t.Fatalf("unexpected client handshake state: connected=%v hello=%+v", c.Connected(), c.Hello())
		}

		attachedCh := make(chan protocol.SessionTarget, 1)
		unsubAtt, err := c.OnAttachmentChange(func(att *protocol.SessionTarget) {
			if att != nil {
				select {
				case attachedCh <- *att:
				default:
				}
			}
		})
		if err != nil {
			t.Fatalf("OnAttachmentChange: %v", err)
		}
		defer unsubAtt()

		rpcCtx, rpcCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer rpcCancel()

		if _, _, err := c.Request(rpcCtx, &protocol.ServerTarget{ServerId: tpbenchRpcServerId}, chord.ServiceCall{
			ServiceId: "pi.session-management",
			Member:    "attach",
			Args:      []chord.JsonValue{"sess-1"},
		}); err != nil {
			t.Fatalf("attach failed: %v", err)
		}
		var target protocol.SessionTarget
		select {
		case target = <-attachedCh:
		case <-rpcCtx.Done():
			t.Fatalf("timed out waiting for AttachmentEnvelope")
		}
		if target.SessionId != "sess-1" || target.AttachmentId == "" {
			t.Fatalf("unexpected SessionTarget: %+v", target)
		}

		catalogue, err := c.ServiceCatalogue(rpcCtx, &target)
		if err != nil {
			t.Fatalf("ServiceCatalogue: %v", err)
		}
		if !slices.ContainsFunc(catalogue, func(e chord.ServiceCatalogueEntry) bool { return e.ServiceId == "bench.counter" }) {
			t.Fatalf("catalogue missing bench.counter: %+v", catalogue)
		}

		var stateMu sync.Mutex
		var replicatedDoc chord.JsonValue = jsonx.ObjectOf("count", float64(0), "log", []any{})
		latestCount := float64(0)
		reachedTen := make(chan struct{})
		var reachedTenOnce sync.Once
		sub, err := c.SubscribeService(rpcCtx, &target, "bench.counter", chord.ServiceModeSingleton, func(u chord.ServiceProviderUpdate) error {
			stateMu.Lock()
			defer stateMu.Unlock()
			switch upd := u.(type) {
			case chord.ServiceProviderUpdateReset:
				if len(upd.Snapshot.Instances) > 0 && len(upd.Snapshot.Instances[0].Members) > 0 {
					if sm, ok := upd.Snapshot.Instances[0].Members[0].(chord.ServiceMemberSnapshotState); ok {
						if applied, err := chord.ApplyImmutableTrusted[chord.JsonValue](replicatedDoc, sm.Ops); err == nil {
							replicatedDoc = applied
						}
					}
				}
			case chord.ServiceProviderUpdateState:
				if applied, err := chord.ApplyImmutableTrusted[chord.JsonValue](replicatedDoc, upd.Ops); err == nil {
					replicatedDoc = applied
				}
			}
			if obj, ok := replicatedDoc.(*jsonx.Object); ok {
				if cntAny, ok := obj.Get("count"); ok {
					if cnt, ok := cntAny.(float64); ok {
						latestCount = cnt
						if latestCount >= 10 {
							reachedTenOnce.Do(func() { close(reachedTen) })
						}
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("SubscribeService: %v", err)
		}
		sub.Start()
		defer func() { _ = sub.Dispose() }()

		const workers = 10
		results := make(chan float64, workers)
		errs := make(chan error, workers)
		var wg sync.WaitGroup
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				val, ok, callErr := c.Request(rpcCtx, &target, chord.ServiceCall{
					ServiceId: "bench.counter",
					Member:    "increment",
					Args:      []chord.JsonValue{float64(1)},
				})
				if callErr != nil {
					errs <- callErr
					return
				}
				if !ok {
					errs <- fmt.Errorf("missing increment result")
					return
				}
				f, ok := val.(float64)
				if !ok {
					errs <- fmt.Errorf("unexpected increment type %T (%v)", val, val)
					return
				}
				results <- f
			}()
		}
		wg.Wait()
		close(results)
		close(errs)
		for err := range errs {
			t.Fatalf("concurrent RPC error: %v", err)
		}
		seen := make(map[int]bool, workers)
		for r := range results {
			seen[int(r)] = true
		}
		for i := 1; i <= workers; i++ {
			if !seen[i] {
				t.Fatalf("missing increment result %d; got %v", i, seen)
			}
		}

		select {
		case <-reachedTen:
		case <-rpcCtx.Done():
			stateMu.Lock()
			got := latestCount
			stateMu.Unlock()
			t.Fatalf("timed out waiting for replicated subscription count==10 (latest=%v)", got)
		}

		if err := sub.Dispose(); err != nil {
			t.Fatalf("sub.Dispose: %v", err)
		}
		if err := c.Dispose(); err != nil {
			t.Fatalf("client.Dispose: %v", err)
		}
	})

	t.Run("durable_sqlite_crash_resume", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "durable.sqlite")
		tpbenchVerifyDurableBackend(t, "sqlite", func() (durable.Storage, error) {
			return sqlite.OpenNodeSqliteStorage(dbPath)
		})
	})

	t.Run("durable_jsonl_crash_resume", func(t *testing.T) {
		dirPath := filepath.Join(t.TempDir(), "durable-jsonl")
		tpbenchVerifyDurableBackend(t, "jsonl", func() (durable.Storage, error) {
			return jsonl.OpenNodeJsonlStorage(dirPath, chord.BackgroundContext)
		})
	})
}

func BenchmarkPort_ProtocolDeltaAndDurable(b *testing.B) {
	b.Run("protocol_and_chord_diff", func(b *testing.B) {
		req := &protocol.RequestEnvelope{
			Type: "request",
			Id:   "bench-1",
			Target: &protocol.SessionTarget{
				ServerId:     tpbenchRpcServerId,
				SessionId:    "sess-bench",
				AttachmentId: "att-bench",
			},
			Call: jsonx.ObjectOf(
				"serviceId", "bench.counter",
				"member", "increment",
				"args", []any{float64(1)},
			),
		}
		rev0 := jsonx.ObjectOf("count", float64(0), "items", []any{"a", "b", "c"})
		rev1 := jsonx.ObjectOf("count", float64(1), "items", []any{"a", "x", "c", "d"})

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			frame, err := protocol.EncodeClientMessage(req, nil)
			if err != nil {
				b.Fatal(err)
			}
			dec, err := protocol.NewClientMessageDecoder(nil)
			if err != nil {
				b.Fatal(err)
			}
			if _, err := dec.Push(frame); err != nil {
				b.Fatal(err)
			}
			ops := chord.DiffRevisions(rev0, rev1)
			if _, err := chord.ApplyImmutableTrusted[chord.JsonValue](rev0, ops); err != nil {
				b.Fatal(err)
			}
		}
	})

	for _, backend := range []string{"sqlite", "jsonl"} {
		b.Run("durable_"+backend+"_commit", func(b *testing.B) {
			ctx := chord.BackgroundContext
			var st durable.Storage
			var err error
			if backend == "sqlite" {
				st, err = sqlite.OpenNodeSqliteStorage(filepath.Join(b.TempDir(), "bench.sqlite"))
			} else {
				st, err = jsonl.OpenNodeJsonlStorage(filepath.Join(b.TempDir(), "bench-jsonl"), ctx)
			}
			if err != nil {
				b.Fatal(err)
			}
			h, err := durable.HarnessOpen(st, durable.HarnessOptions{Registry: durable.CreateRegistry()}, ctx)
			if err != nil {
				b.Fatal(err)
			}
			defer func() { _ = h.Close(ctx) }()
			root, err := h.Root(ctx)
			if err != nil {
				b.Fatal(err)
			}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				text := fmt.Sprintf("bench message %d", i)
				if _, err := root.Commit(ctx, func(tx durable.Tx) (any, error) {
					return tx.AppendEntry(root.Id(), durable.EntryDraft{
						Kind: "message",
						Model: ai.MessageList{&ai.UserMessage{
							Role: "user", Content: ai.UserMessageContent{Text: &text}, Timestamp: int64(i),
						}},
					})
				}); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
		})
	}
}
