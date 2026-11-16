package core

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/typebox"
)

func telStrPtr(s string) *string { return &s }

func telSnapshotBuiltins(t *testing.T) {
	t.Helper()
	orig := GetDefaultBuiltinExtensions()
	t.Cleanup(func() {
		SetDefaultBuiltinExtensions(orig)
	})
}

func telDummyExec(text string) ToolExecuteFn {
	return func(_ context.Context, _ string, _ *jsonx.Object, _ agent.AgentToolUpdateCallback, _ ExtensionToolContext) (agent.AgentToolResult, error) {
		return agent.AgentToolResult{
			Content: ai.ContentList{&ai.TextContent{Type: "text", Text: text}},
		}, nil
	}
}

func TestExtensionsLoaderDefaultBuiltinRegistry(t *testing.T) {
	telSnapshotBuiltins(t)

	SetDefaultBuiltinExtensions([]InlineExtension{
		{
			Name:        "builtin:alpha",
			Builtin:     true,
			Replaceable: true,
			Factory: func(pi ExtensionAPI) error {
				pi.RegisterTool(ToolDefinition{Name: "alpha_tool", Description: "alpha", Execute: telDummyExec("alpha")})
				return nil
			},
		},
	})

	got := GetDefaultBuiltinExtensions()
	if len(got) != 1 || got[0].Name != "builtin:alpha" {
		t.Fatalf("unexpected builtin list: %+v", got)
	}
	// Returned slice must be a detached copy.
	got[0].Name = "mutated"
	again := GetDefaultBuiltinExtensions()
	if again[0].Name != "builtin:alpha" {
		t.Fatalf("expected snapshot copy, got mutated entry: %+v", again)
	}

	// Concurrent read/write must be race-free.
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			SetDefaultBuiltinExtensions([]InlineExtension{{Name: "builtin:alpha", Builtin: true}})
			_ = GetDefaultBuiltinExtensions()
			_ = idx
		}(i)
	}
	wg.Wait()
}

func TestExtensionsLoaderFactoryAndRollback(t *testing.T) {
	ctx := context.Background()
	bus := CreateEventBus()

	ext, err := LoadExtensionFromFactory(ctx, InlineExtension{
		Name: "inline:good",
		Factory: func(pi ExtensionAPI) error {
			pi.RegisterFlag("good-flag", ExtensionFlagOptions{Type: "boolean", Default: true})
			pi.RegisterTool(ToolDefinition{
				Name:        "good_tool",
				Description: "good",
				Parameters:  typebox.Object(nil, nil),
				Execute:     telDummyExec("ok"),
			})
			pi.RegisterCommand("goodcmd", RegisteredCommand{Description: telStrPtr("good cmd")})
			pi.OnInput(func(_ context.Context, _ *InputEvent, _ ExtensionContext) (*InputEventResult, error) {
				return &InputEventResult{Action: "continue"}, nil
			})
			pi.Events().On("loading-topic", func(any) {})
			return nil
		},
	}, t.TempDir(), bus)
	if err != nil {
		t.Fatalf("LoadExtensionFromFactory failed: %v", err)
	}
	if _, ok := ext.Tools["good_tool"]; !ok {
		t.Fatalf("expected good_tool registered")
	}
	if _, ok := ext.Commands["goodcmd"]; !ok {
		t.Fatalf("expected goodcmd registered")
	}
	if len(ext.Handlers["input"]) != 1 {
		t.Fatalf("expected 1 input handler, got %d", len(ext.Handlers["input"]))
	}

	// Verify rollback on both error return and panic during load.
	for _, tc := range []struct {
		name string
		fail func() error
	}{
		{
			name: "returned_error",
			fail: func() error { return os.ErrInvalid },
		},
		{
			name: "panic",
			fail: func() error { panic("boom during load") },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := CreateExtensionRuntime()
			localBus := CreateEventBus()
			var busFired int
			_, err := LoadExtensionFromFactoryWithRuntime(ctx, InlineExtension{
				Name: "inline:bad-" + tc.name,
				Factory: func(pi ExtensionAPI) error {
					pi.RegisterFlag("bad-flag", ExtensionFlagOptions{Type: "string", Default: "x"})
					pi.RegisterProvider("bad-prov", ProviderConfig{BaseUrl: telStrPtr("https://example.invalid")})
					url := "https://mcp.invalid"
					pi.RegisterMcpServer("bad-mcp", McpServerConfig{Url: &url})
					pi.Events().On("rollback-ev", func(any) { busFired++ })
					return tc.fail()
				},
			}, t.TempDir(), rt, localBus)
			if err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
			if _, exists := rt.FlagValues["bad-flag"]; exists {
				t.Fatalf("flag should be rolled back on failed load")
			}
			if len(rt.PendingProviderRegistrations) != 0 {
				t.Fatalf("pending provider registrations should be rolled back, got %d", len(rt.PendingProviderRegistrations))
			}
			if len(rt.McpServers.List()) != 0 {
				t.Fatalf("MCP server registrations should be rolled back, got %d", len(rt.McpServers.List()))
			}
			localBus.Emit("rollback-ev", "ping")
			if busFired != 0 {
				t.Fatalf("expected event bus subscription rolled back, fired %d times", busFired)
			}
		})
	}
}

func TestExtensionsLoaderDiscoverAndLoad(t *testing.T) {
	telSnapshotBuiltins(t)
	ctx := context.Background()

	root := t.TempDir()
	agentDir := filepath.Join(root, "agent")
	cwd := filepath.Join(root, "project")

	// <agentDir>/extensions/dir-ext/index.ts
	dirExtDir := filepath.Join(agentDir, "extensions", "dir-ext")
	if err := os.MkdirAll(dirExtDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirExtDir, "index.ts"), []byte(`
export default function (pi: any) {
  pi.registerCommand("from-dir-index", { description: "dir index", handler: async () => {} });
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// <agentDir>/extensions/single.js
	singleJS := filepath.Join(agentDir, "extensions", "single.js")
	if err := os.WriteFile(singleJS, []byte(`
export default function (pi) {
  pi.registerCommand("from-single-js", { description: "single js", handler: async () => {} });
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// <cwd>/.pi/extensions/pkg-ext/package.json -> src/entry.ts
	pkgDir := filepath.Join(cwd, ".pi", "extensions", "pkg-ext")
	if err := os.MkdirAll(filepath.Join(pkgDir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "package.json"), []byte(`{"name":"pkg-ext","pi":{"extensions":["src/entry.ts"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "src", "entry.ts"), []byte(`
export default function (pi: any) {
  pi.registerCommand("from-pkg-json", { description: "pkg", handler: async () => {} });
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Explicit configured path + symlink duplicate pointing to single.js (must dedup).
	explicitTS := filepath.Join(root, "explicit.ts")
	if err := os.WriteFile(explicitTS, []byte(`
export default function (pi: any) {
  pi.registerCommand("from-configured", { description: "configured", handler: async () => {} });
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	symlinkDup := filepath.Join(root, "single-link.js")
	if err := os.Symlink(singleJS, symlinkDup); err != nil {
		t.Fatal(err)
	}

	// Register one enabled and one disabled built-in.
	SetDefaultBuiltinExtensions([]InlineExtension{
		{
			Name:    "enabled-builtin",
			Builtin: true,
			Factory: func(pi ExtensionAPI) error {
				pi.RegisterCommand("from-enabled-builtin", RegisteredCommand{Description: telStrPtr("enabled")})
				return nil
			},
		},
		{
			Name:    "disabled-builtin",
			Builtin: true,
			Factory: func(pi ExtensionAPI) error {
				pi.RegisterCommand("from-disabled-builtin", RegisteredCommand{Description: telStrPtr("disabled")})
				return nil
			},
		},
	})

	// Test 5-arg DiscoverAndLoadExtensions backward-compat signature first.
	resCompat := DiscoverAndLoadExtensions(ctx, []string{explicitTS}, cwd, agentDir)
	defer ShutdownExtensionRuntime(resCompat.Runtime)
	if len(resCompat.Errors) != 0 {
		t.Fatalf("DiscoverAndLoadExtensions errors: %+v", resCompat.Errors)
	}

	res := DiscoverAndLoadExtensionsWithOptions(ctx, []string{explicitTS, symlinkDup, "builtin:enabled-builtin"}, &LoadExtensionsOptions{
		Cwd:                       cwd,
		AgentDir:                  agentDir,
		DisabledBuiltinExtensions: []string{"disabled-builtin"},
	})
	defer ShutdownExtensionRuntime(res.Runtime)
	if len(res.Errors) != 0 {
		t.Fatalf("DiscoverAndLoadExtensionsWithOptions errors: %+v", res.Errors)
	}

	cmds := map[string]int{}
	for _, ext := range res.Extensions {
		for name := range ext.Commands {
			cmds[name]++
		}
	}
	for _, want := range []string{"from-dir-index", "from-single-js", "from-pkg-json", "from-configured", "from-enabled-builtin"} {
		if cmds[want] != 1 {
			t.Fatalf("command %q count = %d, want 1 (all cmds: %v)", want, cmds[want], cmds)
		}
	}
	if cmds["from-disabled-builtin"] != 0 {
		t.Fatalf("disabled builtin should not have been loaded, cmds=%v", cmds)
	}
}

func TestExtensionsLoaderBundleCacheAndInvalidation(t *testing.T) {
	ClearExtensionCache()
	t.Cleanup(ClearExtensionCache)

	ctx := context.Background()
	dir := t.TempDir()
	extPath := filepath.Join(dir, "cached.ts")

	writeVer := func(ver string, mtime time.Time) {
		src := `
export default function (pi: any) {
  pi.registerTool({
    name: "cached_tool",
    label: "Cached",
    description: "returns version",
    parameters: { type: "object", properties: {} },
    async execute() {
      return { content: [{ type: "text", text: "` + ver + `" }] };
    },
  });
}
`
		if err := os.WriteFile(extPath, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(extPath, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}

	execCached := func(res LoadExtensionsResult) string {
		runner := NewExtensionRunner(res.Extensions, res.Runtime, dir, nil, nil)
		def := runner.GetToolDefinition("cached_tool")
		if def == nil {
			t.Fatalf("cached_tool not registered")
		}
		out, err := def.Execute(ctx, "c1", jsonx.NewObject(), nil, runner.CreateToolContext("c1", ctx))
		if err != nil {
			t.Fatalf("execute failed: %v", err)
		}
		return out.Content[0].(*ai.TextContent).Text
	}

	t0 := time.Now().Add(-10 * time.Second).Truncate(time.Second)
	writeVer("v1", t0)

	r1 := LoadExtensionsCached(ctx, []string{extPath}, dir)
	r2 := LoadExtensionsCached(ctx, []string{extPath}, dir)
	if len(r1.Errors) != 0 || len(r2.Errors) != 0 {
		t.Fatalf("unexpected load errors: r1=%+v r2=%+v", r1.Errors, r2.Errors)
	}
	if r1.Runtime == r2.Runtime {
		t.Fatalf("each LoadExtensionsCached call must create a fresh ExtensionRuntime")
	}

	// Shutting down runtime 1 must leave runtime 2 / VM 2 intact.
	ShutdownExtensionRuntime(r1.Runtime)
	defer ShutdownExtensionRuntime(r2.Runtime)
	if got := execCached(r2); got != "v1" {
		t.Fatalf("expected v1 from r2 after r1 shutdown, got %q", got)
	}

	// Update file with a newer mtime -> cache invalidates automatically.
	writeVer("v2-longer", t0.Add(5*time.Second))
	r3 := LoadExtensionsCached(ctx, []string{extPath}, dir)
	defer ShutdownExtensionRuntime(r3.Runtime)
	if len(r3.Errors) != 0 {
		t.Fatalf("r3 errors: %+v", r3.Errors)
	}
	if got := execCached(r3); got != "v2-longer" {
		t.Fatalf("expected v2-longer after mtime update, got %q", got)
	}

	// Rewrite with identical mtime + length, then verify ClearExtensionCache forces re-bundle.
	writeVer("v3-longer", t0.Add(5*time.Second))
	ClearExtensionCache()
	r4 := LoadExtensionsCached(ctx, []string{extPath}, dir)
	defer ShutdownExtensionRuntime(r4.Runtime)
	if len(r4.Errors) != 0 {
		t.Fatalf("r4 errors: %+v", r4.Errors)
	}
	if got := execCached(r4); got != "v3-longer" {
		t.Fatalf("expected v3-longer after ClearExtensionCache, got %q", got)
	}
}

func TestExtensionsLoaderOmitReplacedAndConflicts(t *testing.T) {
	ctx := context.Background()
	cwd := t.TempDir()

	builtinReplaced, err := LoadExtensionFromFactory(ctx, InlineExtension{
		Name:        "builtin:search",
		Builtin:     true,
		Replaceable: true,
		Factory: func(pi ExtensionAPI) error {
			pi.RegisterTool(ToolDefinition{Name: "search", Description: "builtin search", Execute: telDummyExec("b")})
			return nil
		},
	}, cwd)
	if err != nil {
		t.Fatal(err)
	}
	builtinKept, err := LoadExtensionFromFactory(ctx, InlineExtension{
		Name:        "builtin:ping",
		Builtin:     true,
		Replaceable: true,
		Factory: func(pi ExtensionAPI) error {
			pi.RegisterCommand("ping", RegisteredCommand{Description: telStrPtr("builtin ping")})
			return nil
		},
	}, cwd)
	if err != nil {
		t.Fatal(err)
	}
	userOverride, err := LoadExtensionFromFactory(ctx, InlineExtension{
		Name: "user-search.ts",
		Factory: func(pi ExtensionAPI) error {
			pi.RegisterTool(ToolDefinition{Name: "search", Description: "user search", Execute: telDummyExec("u")})
			pi.RegisterFlag("mode", ExtensionFlagOptions{Type: "string"})
			return nil
		},
	}, cwd)
	if err != nil {
		t.Fatal(err)
	}
	userDup, err := LoadExtensionFromFactory(ctx, InlineExtension{
		Name: "user-dup.ts",
		Factory: func(pi ExtensionAPI) error {
			pi.RegisterTool(ToolDefinition{Name: "search", Description: "dup"})
			pi.RegisterCommand("dup-cmd", RegisteredCommand{})
			pi.RegisterFlag("mode", ExtensionFlagOptions{Type: "string"})
			return nil
		},
	}, cwd)
	if err != nil {
		t.Fatal(err)
	}
	userDupCmd, err := LoadExtensionFromFactory(ctx, InlineExtension{
		Name: "user-dup-cmd.ts",
		Factory: func(pi ExtensionAPI) error {
			pi.RegisterCommand("dup-cmd", RegisteredCommand{})
			return nil
		},
	}, cwd)
	if err != nil {
		t.Fatal(err)
	}

	kept, warnings := OmitReplacedExtensions([]Extension{*builtinReplaced, *builtinKept, *userOverride})
	if len(kept) != 2 {
		t.Fatalf("expected 2 kept extensions, got %d (%+v)", len(kept), kept)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0].Warning, "search") {
		t.Fatalf("expected 1 replacement warning referencing search, got %+v", warnings)
	}
	var keptPaths []string
	for _, k := range kept {
		keptPaths = append(keptPaths, k.Path)
	}
	if !slices.Contains(keptPaths, "builtin:ping") || !slices.Contains(keptPaths, "user-search.ts") {
		t.Fatalf("unexpected kept paths: %v", keptPaths)
	}

	errs := DetectExtensionConflicts([]Extension{*userOverride, *userDup, *userDupCmd})
	if len(errs) != 3 {
		t.Fatalf("expected 3 conflicts (tool, flag, command), got %d: %+v", len(errs), errs)
	}
}

func TestExtensionsLoaderMcpValidationAndCollision(t *testing.T) {
	ctx := context.Background()
	cwd := t.TempDir()

	url := "https://mcp.example.com/sse"
	cmd := "npx"

	// Valid URL server works.
	rt := CreateExtensionRuntime()
	_, err := LoadExtensionFromFactoryWithRuntime(ctx, InlineExtension{
		Name: "mcp-valid",
		Factory: func(pi ExtensionAPI) error {
			pi.RegisterMcpServer("my-server", McpServerConfig{Url: &url})
			got := pi.GetMcpServers()
			if len(got) != 1 || got[0].Name != "my-server" {
				t.Fatalf("unexpected GetMcpServers result: %+v", got)
			}
			return nil
		},
	}, cwd, rt)
	if err != nil {
		t.Fatalf("valid MCP server failed: %v", err)
	}

	// Both command and url must fail validation cleanly.
	if _, err := LoadExtensionFromFactory(ctx, InlineExtension{
		Name: "mcp-both",
		Factory: func(pi ExtensionAPI) error {
			pi.RegisterMcpServer("bad", McpServerConfig{Command: &cmd, Url: &url})
			return nil
		},
	}, cwd); err == nil {
		t.Fatalf("expected validation error when both command and url are set")
	}

	// Neither command nor url must fail validation cleanly.
	if _, err := LoadExtensionFromFactory(ctx, InlineExtension{
		Name: "mcp-neither",
		Factory: func(pi ExtensionAPI) error {
			pi.RegisterMcpServer("empty", McpServerConfig{})
			return nil
		},
	}, cwd); err == nil {
		t.Fatalf("expected validation error when neither command nor url is set")
	}

	// Namespace collision ("my_server" maps to same mcp namespace as "my-server").
	if _, err := LoadExtensionFromFactoryWithRuntime(ctx, InlineExtension{
		Name: "mcp-clash",
		Factory: func(pi ExtensionAPI) error {
			pi.RegisterMcpServer("my_server", McpServerConfig{Command: &cmd})
			return nil
		},
	}, cwd, rt); err == nil {
		t.Fatalf("expected MCP namespace collision error")
	}

	// Same extension may replace its own exact-name registration, while another extension cannot.
	url2 := "https://mcp2.example.com/sse"
	if _, err := LoadExtensionFromFactoryWithRuntime(ctx, InlineExtension{
		Name: "mcp-valid",
		Factory: func(pi ExtensionAPI) error {
			pi.RegisterMcpServer("my-server", McpServerConfig{Url: &url2})
			return nil
		},
	}, cwd, rt); err != nil {
		t.Fatalf("same extension updating its own MCP server should succeed, got: %v", err)
	}
	if reg := rt.McpServers.Get("my-server"); reg == nil || reg.Config.Url == nil || *reg.Config.Url != url2 {
		t.Fatalf("expected same extension update to replace URL with %q, got %+v", url2, reg)
	}
	if _, err := LoadExtensionFromFactoryWithRuntime(ctx, InlineExtension{
		Name: "mcp-steal",
		Factory: func(pi ExtensionAPI) error {
			pi.RegisterMcpServer("my-server", McpServerConfig{Command: &cmd})
			return nil
		},
	}, cwd, rt); err == nil || !strings.Contains(err.Error(), `MCP server "my-server" is already registered by extension`) {
		t.Fatalf("expected cross-extension exact-name MCP error, got: %v", err)
	}
	if reg := rt.McpServers.Get("my-server"); reg == nil || reg.ExtensionPath != "mcp-valid" {
		t.Fatalf("expected failed registration not to overwrite original owner, got %+v", reg)
	}
}
