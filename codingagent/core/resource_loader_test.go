package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/utils"
)

func TestDefaultResourceLoaderDiscoversResourcesAndExtensions(t *testing.T) {
	base := t.TempDir()
	t.Setenv("HOME", base)
	cwd := filepath.Join(base, "project")
	agentDir := filepath.Join(base, "agent")

	// Set up project + user resources + context files + SYSTEM/APPEND_SYSTEM.
	for _, d := range []string{
		filepath.Join(cwd, ".pi", "skills", "proj-skill"),
		filepath.Join(cwd, ".pi", "prompts"),
		filepath.Join(cwd, ".pi", "extensions", "fake-ext"),
		filepath.Join(agentDir, "skills", "user-skill"),
		filepath.Join(agentDir, "prompts"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(cwd, ".pi", "skills", "proj-skill", "SKILL.md"), []byte("---\nname: proj-skill\ndescription: Project skill\n---\nBody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "skills", "user-skill", "SKILL.md"), []byte("---\nname: user-skill\ndescription: User skill\n---\nBody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".pi", "prompts", "deploy.md"), []byte("---\ndescription: Deploy\n---\nDeploy $1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "prompts", "lint.md"), []byte("Lint project\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".pi", "extensions", "fake-ext", "index.ts"), []byte("export default function(pi) { pi.registerCommand('fake-cmd', { description: 'Fake', handler: async () => {} }); }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "AGENTS.md"), []byte("# Project instructions\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "AGENTS.md"), []byte("# Global instructions\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".pi", "SYSTEM.md"), []byte("Project system prompt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "APPEND_SYSTEM.md"), []byte("Global append\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sm := SettingsManagerInMemory()
	sm.SetProjectTrusted(true)

	loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{
		Cwd:             cwd,
		AgentDir:        agentDir,
		SettingsManager: sm,
		EventBus:        CreateEventBus(),
	})
	defer loader.Close()
	if loader.GetPackageManager() == nil {
		t.Fatalf("expected non-nil GetPackageManager()")
	}
	if err := loader.Reload(context.Background()); err != nil {
		t.Fatalf("Reload failed: %v", err)
	}

	exts := loader.GetExtensions()
	if len(exts.Extensions) != 1 || len(exts.Errors) != 0 {
		t.Fatalf("expected 1 loaded TS extension, got %+v", exts)
	}
	if _, ok := exts.Extensions[0].Commands["fake-cmd"]; !ok {
		t.Fatalf("expected fake-cmd registered from .pi/extensions/fake-ext/index.ts, got %+v", exts.Extensions[0].Commands)
	}

	if got := len(loader.GetSkills().Skills); got != 2 {
		t.Fatalf("expected 2 skills, got %d (%+v)", got, loader.GetSkills().Skills)
	}
	if got := len(loader.GetPrompts().Templates); got != 2 {
		t.Fatalf("expected 2 prompts, got %d", got)
	}
	if got := len(loader.GetAgentsFiles().AgentsFiles); got != 2 {
		t.Fatalf("expected 2 context files, got %d", got)
	}
	sys := loader.GetSystemPrompt()
	if sys == nil || strings.TrimSpace(*sys) != "Project system prompt" {
		t.Fatalf("expected project SYSTEM.md, got %v", sys)
	}
	if src := loader.GetSystemPromptSource(); src == nil || !strings.HasSuffix(src.Path, filepath.Join(".pi", "SYSTEM.md")) {
		t.Fatalf("unexpected system prompt source: %+v", src)
	}
	appends := loader.GetAppendSystemPrompt()
	if len(appends) != 1 || strings.TrimSpace(appends[0]) != "Global append" {
		t.Fatalf("expected global append prompt, got %+v", appends)
	}

	// Dynamic resource additions via ExtendResources.
	extraSkill := filepath.Join(base, "dynamic", "dyn-skill.md")
	if err := os.MkdirAll(filepath.Dir(extraSkill), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(extraSkill, []byte("---\nname: dyn-skill\ndescription: Dynamic skill\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	loader.ExtendResources(ResourceExtensionPaths{
		SkillPaths: []ResourceExtensionPath{{
			Path: extraSkill,
			Metadata: utils.PathMetadata{
				Source: "extension:dyn",
				Scope:  utils.SourceScopeTemporary,
				Origin: utils.SourceOriginTopLevel,
			},
		}},
	})
	if got := len(loader.GetSkills().Skills); got != 3 {
		t.Fatalf("expected 3 skills after ExtendResources, got %d", got)
	}
}

func TestDefaultResourceLoaderExclusionsTrustAndOverrides(t *testing.T) {
	base := t.TempDir()
	t.Setenv("HOME", base)
	cwd := filepath.Join(base, "project")
	agentDir := filepath.Join(base, "agent")

	for _, name := range []string{"keep-me", "skip-me"} {
		d := filepath.Join(agentDir, "skills", name)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: Skill "+name+"\n---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(cwd, ".pi"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".pi", "SYSTEM.md"), []byte("Project-only secret system prompt\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sm := SettingsManagerInMemory()
	sm.SetProjectTrusted(false)
	sm.SetSkillPaths([]string{"!**/skip-me/**"})

	loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{
		Cwd:             cwd,
		AgentDir:        agentDir,
		SettingsManager: sm,
		NoContextFiles:  true,
		AppendSystemPromptOverride: func(prev []string) []string {
			return append(prev, "custom-append")
		},
	})
	if err := loader.Reload(context.Background()); err != nil {
		t.Fatalf("Reload failed: %v", err)
	}

	skills := loader.GetSkills().Skills
	if len(skills) != 1 || skills[0].Name != "keep-me" {
		t.Fatalf("expected only keep-me skill with !**/skip-me/** exclusion, got %+v", skills)
	}
	// Project SYSTEM.md must not load when projectTrusted=false.
	if loader.GetSystemPrompt() != nil {
		t.Fatalf("expected nil project system prompt when untrusted, got %q", *loader.GetSystemPrompt())
	}
	if len(loader.GetAgentsFiles().AgentsFiles) != 0 {
		t.Fatalf("expected 0 context files when NoContextFiles=true")
	}
	if appends := loader.GetAppendSystemPrompt(); len(appends) != 1 || appends[0] != "custom-append" {
		t.Fatalf("expected custom-append override, got %+v", appends)
	}
}

func TestLoadProjectContextFilesOrderAndWorktreeDedup(t *testing.T) {
	root := t.TempDir()
	agentDir := filepath.Join(root, "agent")
	repo := filepath.Join(root, "workspace", "repo")
	sub := filepath.Join(repo, "sub")
	for _, d := range []string{agentDir, sub} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(agentDir, "AGENTS.md"), []byte("global\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "CLAUDE.md"), []byte("repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "AGENTS.override.md"), []byte("sub\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	files := LoadProjectContextFiles(&LoadProjectContextFilesOptions{
		Cwd:      sub,
		AgentDir: agentDir,
	})
	if len(files) != 3 {
		t.Fatalf("expected 3 ordered context files, got %+v", files)
	}
	if filepath.Base(files[0].Path) != "AGENTS.md" || filepath.Base(files[1].Path) != "CLAUDE.md" || filepath.Base(files[2].Path) != "AGENTS.override.md" {
		t.Fatalf("unexpected context file order/precedence: %+v", files)
	}
}

func TestDefaultPackageManagerResourceLoaderAndMcpRegistry(t *testing.T) {
	base := t.TempDir()
	t.Setenv("HOME", base)
	cwd := filepath.Join(base, "proj")
	agentDir := filepath.Join(base, "agent")
	pkgDir := filepath.Join(base, "local-pkg")
	for _, d := range []string{
		filepath.Join(pkgDir, "skills", "pkg-skill"),
		filepath.Join(pkgDir, "prompts"),
		filepath.Join(cwd, ".pi", "extensions"),
		agentDir,
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "package.json"), []byte(`{
		"name": "local-pkg",
		"pi": {
			"extensions": ["pkg-ext.ts"],
			"skills": ["skills"],
			"prompts": ["prompts"]
		}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "pkg-ext.ts"), []byte(`export default function(pi) {
		pi.registerFlag("pkg-opt", { type: "string", default: "ok" });
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "skills", "pkg-skill", "SKILL.md"), []byte("---\nname: pkg-skill\ndescription: Pkg skill\n---\nPkg\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "prompts", "pkg-prompt.md"), []byte("---\ndescription: Pkg prompt\n---\nHello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".pi", "extensions", "proj-ext.ts"), []byte(`export default function(pi) {
		pi.registerCommand("proj-cmd", { description: "Proj", handler: async () => {} });
	}`), 0o644); err != nil {
		t.Fatal(err)
	}

	sm := SettingsManagerInMemory()
	sm.SetProjectTrusted(false)
	pm := NewDefaultPackageManager(DefaultPackageManagerOptions{
		Cwd:             cwd,
		AgentDir:        agentDir,
		SettingsManager: sm,
	})
	if pkgs := pm.GetInstalledPackages(); len(pkgs) != 0 {
		t.Fatalf("expected empty installed packages, got %+v", pkgs)
	}
	if err := pm.Install(t.Context(), filepath.Join(base, "does-not-exist")); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("Install missing local path err = %v, want does-not-exist error", err)
	}
	pm.AddSourceToSettings(pkgDir, false)

	resolved, err := pm.Resolve(t.Context())
	if err != nil {
		t.Fatalf("Resolve err=%v", err)
	}
	if len(resolved.Extensions) != 1 || len(resolved.Skills) != 1 || len(resolved.Prompts) != 1 {
		t.Fatalf("expected package resources resolved, got %+v", resolved)
	}

	loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{
		Cwd:             cwd,
		AgentDir:        agentDir,
		SettingsManager: sm,
	})
	defer loader.Close()

	// ResolveProjectTrust initially receives only trusted global/package extensions, then approves project trust.
	var trustBootstrapCount int
	if err := loader.Reload(t.Context(), &ResourceLoaderReloadOptions{
		ResolveProjectTrust: func(_ context.Context, input ResolveProjectTrustInput) (bool, error) {
			trustBootstrapCount = len(input.ExtensionsResult.Extensions)
			return true, nil
		},
	}); err != nil {
		t.Fatalf("Reload with trust callback failed: %v", err)
	}
	if trustBootstrapCount != 1 {
		t.Fatalf("expected 1 untrusted bootstrap extension (global pkg only), got %d", trustBootstrapCount)
	}
	exts := loader.GetExtensions()
	if len(exts.Extensions) != 2 || len(exts.Errors) != 0 {
		t.Fatalf("expected 2 extensions after project approval (pkg + .pi/extensions), got %+v", exts)
	}
	pkgExtPath := filepath.Join(pkgDir, "pkg-ext.ts")
	meta := loader.GetPathMetadata()
	if m, ok := meta[pkgExtPath]; !ok || m.Origin != utils.SourceOriginPackage {
		t.Fatalf("expected package Origin metadata for %s, got %+v (ok=%v)", pkgExtPath, m, ok)
	}
	if len(loader.GetSkills().Skills) != 1 || loader.GetSkills().Skills[0].Name != "pkg-skill" {
		t.Fatalf("expected pkg-skill loaded, got %+v", loader.GetSkills().Skills)
	}
	if len(loader.GetPrompts().Templates) != 1 || loader.GetPrompts().Templates[0].Name != "pkg-prompt" {
		t.Fatalf("expected pkg-prompt loaded, got %+v", loader.GetPrompts().Templates)
	}

	// Verify CreateAgentSessionServices end-to-end wires TS extension flags, providers, virtual models & Dispose cleanup.
	svcExtPath := filepath.Join(base, "svc-ext.ts")
	if err := os.WriteFile(svcExtPath, []byte(`export default function(pi) {
		pi.registerFlag("svc-flag", { type: "string", default: "init" });
		pi.registerProvider("ext-prov", {
			baseUrl: "https://example.invalid/v1",
			apiKey: "test-key",
			api: "openai-completions",
			models: [{ id: "ext-m1", name: "Ext M1", reasoning: false, input: ["text"], contextWindow: 8192, maxTokens: 1024 }]
		});
		pi.registerVirtualModel({
			provider: "ext-virt",
			id: "router-1",
			name: "Router 1",
			route: () => ({ provider: "ext-prov", modelId: "ext-m1" })
		});
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	services, err := CreateAgentSessionServices(t.Context(), &CreateAgentSessionServicesOptions{
		Cwd:                      cwd,
		AgentDir:                 agentDir,
		SettingsManager:          sm,
		NoExtensions:             true,
		AdditionalExtensionPaths: []string{svcExtPath},
		FlagValues:               map[string]any{"svc-flag": "override"},
	})
	if err != nil {
		t.Fatalf("CreateAgentSessionServices failed: %v", err)
	}
	defer func() {
		if dl, ok := services.ResourceLoader.(*DefaultResourceLoader); ok {
			dl.Close()
		}
	}()
	if len(services.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", services.Diagnostics)
	}
	if got := services.ResourceLoader.GetExtensions().Runtime.FlagValues["svc-flag"]; got != "override" {
		t.Fatalf("expected svc-flag=override, got %v", got)
	}
	if m := services.ModelRuntime.GetModel("ext-prov", "ext-m1"); m == nil {
		t.Fatalf("expected registered extension provider model ext-prov/ext-m1")
	}
	if vm := services.ModelRuntime.GetModel("ext-virt", "router-1"); vm == nil {
		t.Fatalf("expected virtual model ext-virt/router-1 in ModelRuntime")
	}

	reg := NewMcpServerRegistry()
	expDirect := McpExposureDirect
	reg.Register(RegisteredMcpServer{
		Name:          "srv",
		ExtensionPath: "/tmp/ext",
		Config:        McpServerConfig{Exposure: &expDirect},
	})
	if len(reg.List()) != 1 || reg.Get("srv") == nil {
		t.Fatalf("expected registered MCP server")
	}
	if got := GetMcpToolExposure(McpServerConfig{Exposure: &expDirect}, "read_mcp_resource"); got != McpExposureDirect {
		t.Fatalf("GetMcpToolExposure = %q, want direct", got)
	}
	reg.Clear()
	if len(reg.List()) != 0 {
		t.Fatalf("expected empty MCP registry after Clear")
	}
}

func TestDefaultResourceLoaderAndSessionWave5Hooks(t *testing.T) {
	base := t.TempDir()
	t.Setenv("HOME", base)
	cwd := filepath.Join(base, "proj")
	agentDir := filepath.Join(base, "agent")
	extraSkillPath := filepath.Join(base, "dyn-skills", "wave5.md")
	for _, d := range []string{cwd, agentDir, filepath.Dir(extraSkillPath)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(extraSkillPath, []byte("---\nname: wave5-dyn\ndescription: Wave5 dyn\n---\nBody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	extPath := filepath.Join(base, "wave5-hooks.ts")
	extSrc := `export default function(pi) {
		pi.on("resources_discover", async () => ({
			skillPaths: [` + "`" + strings.ReplaceAll(extraSkillPath, `\`, `\\`) + "`" + `],
		}));
		pi.on("context", async (event) => {
			return {
				messages: event.messages.map((m) =>
					m.role === "user"
						? { ...m, content: [{ type: "text", text: "rewritten" }] }
						: m
				),
			};
		});
		pi.on("before_provider_request", async (event) => ({
			...event.payload,
			wave5Hooked: true,
		}));
	}`
	if err := os.WriteFile(extPath, []byte(extSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	sm := SettingsManagerInMemory()
	sm.SetProjectTrusted(true)

	res, err := CreateAgentSession(t.Context(), &CreateAgentSessionOptions{
		Cwd:                      cwd,
		AgentDir:                 agentDir,
		SettingsManager:          sm,
		AuthStorage:              AuthStorageInMemory(),
		NoExtensions:             true,
		NoSkills:                 true,
		NoPromptTemplates:        true,
		NoThemes:                 true,
		NoContextFiles:           true,
		AdditionalExtensionPaths: []string{extPath},
	})
	if err != nil {
		t.Fatalf("CreateAgentSession failed: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", res.Diagnostics)
	}

	var hostShutdownCalled bool
	if err := res.Session.BindExtensions(t.Context(), ExtensionBindings{
		ShutdownHandler: func() { hostShutdownCalled = true },
	}); err != nil {
		t.Fatalf("BindExtensions failed: %v", err)
	}

	skills := res.Session.ResourceLoader().GetSkills().Skills
	if len(skills) != 1 || skills[0].Name != "wave5-dyn" {
		t.Fatalf("expected wave5-dyn skill from resources_discover, got %+v", skills)
	}

	tc := res.Session.Agent.TransformContext()
	if tc == nil {
		t.Fatalf("expected non-nil TransformContext on Agent")
	}
	userMsg := &ai.UserMessage{
		Role:      "user",
		Content:   ai.UserMessageContent{Blocks: ai.ContentList{&ai.TextContent{Type: "text", Text: "orig"}}},
		Timestamp: 1,
	}
	transformed, err := tc(t.Context(), []agent.AgentMessage{userMsg})
	if err != nil {
		t.Fatalf("TransformContext failed: %v", err)
	}
	if len(transformed) != 1 {
		t.Fatalf("expected 1 transformed message, got %d", len(transformed))
	}
	if um, ok := transformed[0].(*ai.UserMessage); !ok || len(um.Content.Blocks) == 0 || um.Content.Blocks[0].(*ai.TextContent).Text != "rewritten" {
		t.Fatalf("expected rewritten text from TS context hook, got %#v", transformed[0])
	}

	onPayload := res.Session.Agent.OnPayload()
	if onPayload == nil {
		t.Fatalf("expected non-nil OnPayload on Agent")
	}
	nextPayload, err := onPayload(map[string]any{"model": "test"}, nil)
	if err != nil {
		t.Fatalf("OnPayload failed: %v", err)
	}
	if pm, ok := nextPayload.(map[string]any); !ok || pm["wave5Hooked"] != true {
		t.Fatalf("expected wave5Hooked=true in payload, got %#v", nextPayload)
	}

	firstRT := res.Session.ResourceLoader().GetExtensions().Runtime
	if err := res.Session.Reload(t.Context()); err != nil {
		t.Fatalf("Session.Reload failed: %v", err)
	}
	secondRT := res.Session.ResourceLoader().GetExtensions().Runtime
	if secondRT == nil || secondRT == firstRT {
		t.Fatalf("expected fresh ExtensionRuntime after Reload")
	}
	erRuntimeCleanupMu.Lock()
	_, firstStillRegistered := erRuntimeCleanups[firstRT]
	erRuntimeCleanupMu.Unlock()
	if firstStillRegistered {
		t.Fatalf("expected first QuickJS runtime cleanups to run during Reload")
	}

	res.Session.Dispose()
	if hostShutdownCalled {
		t.Fatalf("Dispose must NOT invoke host ShutdownHandler")
	}
	erRuntimeCleanupMu.Lock()
	_, secondStillRegistered := erRuntimeCleanups[secondRT]
	erRuntimeCleanupMu.Unlock()
	if secondStillRegistered {
		t.Fatalf("expected second QuickJS runtime cleanups to run during Dispose")
	}
}
