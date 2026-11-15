package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/utils"
)

func TestDefaultResourceLoaderBareboneResourcesAndNoExtensions(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(cwd, ".pi", "extensions", "fake-ext", "index.ts"), []byte("export default function() {}\n"), 0o644); err != nil {
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
	if err := loader.Reload(context.Background()); err != nil {
		t.Fatalf("Reload failed: %v", err)
	}

	// Barebone build: zero extensions even when .pi/extensions/*.ts exists on disk.
	exts := loader.GetExtensions()
	if len(exts.Extensions) != 0 || len(exts.Errors) != 0 {
		t.Fatalf("expected barebone empty extensions, got %+v", exts)
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

func TestBarebonePackageManagerAndMcpStubs(t *testing.T) {
	pm := NewDefaultPackageManager(DefaultPackageManagerOptions{
		Cwd:      t.TempDir(),
		AgentDir: t.TempDir(),
	})
	if pkgs := pm.GetInstalledPackages(); len(pkgs) != 0 {
		t.Fatalf("expected empty installed packages, got %+v", pkgs)
	}
	if err := pm.Install(t.Context(), "npm:example"); err != ErrPackagesUnsupported {
		t.Fatalf("Install err = %v, want ErrPackagesUnsupported", err)
	}
	if err := pm.Remove(t.Context(), "npm:example"); err != ErrPackagesUnsupported {
		t.Fatalf("Remove err = %v, want ErrPackagesUnsupported", err)
	}
	if err := pm.Update(t.Context()); err != ErrPackagesUnsupported {
		t.Fatalf("Update err = %v, want ErrPackagesUnsupported", err)
	}
	resolved, err := pm.Resolve(t.Context())
	if err != nil || len(resolved.Extensions) != 0 || len(resolved.Skills) != 0 {
		t.Fatalf("Resolve = %+v err=%v", resolved, err)
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
