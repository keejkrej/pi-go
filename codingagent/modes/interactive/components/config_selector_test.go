// Ported from packages/coding-agent/test/config-selector-keybindings.test.ts (pi v1.5.2).

package components

import (
	"slices"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/tui"
)

func tcsSetupEnv(t *testing.T) {
	t.Helper()
	prevKB := tui.GetKeybindings()
	tui.SetKeybindings(core.NewKeybindingsManager(nil).KeybindingsManager)
	_, _ = theme.InitTheme("dark", false)
	t.Cleanup(func() {
		tui.SetKeybindings(prevKB)
	})
}

func TestConfigSelectorComponent(t *testing.T) {
	tcsSetupEnv(t)

	t.Run("routes vim-like letters j/k/g to search input rather than list navigation", func(t *testing.T) {
		closed := 0
		exited := 0
		sm := core.SettingsManagerInMemory()

		scoped := ScopedResolvedPaths{
			Global: core.ResolvedPaths{
				Extensions: []core.ResolvedResource{
					{
						Path:    "/tmp/agent/extensions/alpha.ts",
						Enabled: true,
						Metadata: utils.PathMetadata{
							Source:  "local",
							Scope:   utils.SourceScopeUser,
							Origin:  utils.SourceOriginTopLevel,
							BaseDir: "/tmp/agent",
						},
					},
					{
						Path:    "/tmp/agent/extensions/jkg-plugin.ts",
						Enabled: true,
						Metadata: utils.PathMetadata{
							Source:  "local",
							Scope:   utils.SourceScopeUser,
							Origin:  utils.SourceOriginTopLevel,
							BaseDir: "/tmp/agent",
						},
					},
				},
			},
		}

		comp := NewConfigSelectorComponent(
			scoped,
			sm,
			"/tmp/proj",
			"/tmp/agent",
			func() { closed++ },
			func() { exited++ },
			nil,
			30,
			ConfigWriteScopeGlobal,
			false,
		)
		comp.SetFocused(true)

		comp.HandleInput("j")
		comp.HandleInput("k")
		comp.HandleInput("g")

		if got := comp.GetResourceList().GetSearchQuery(); got != "jkg" {
			t.Fatalf("expected j/k/g typed into search query, got %q", got)
		}
		if closed != 0 || exited != 0 {
			t.Fatalf("expected no close/exit on j/k/g, got closed=%d exited=%d", closed, exited)
		}

		out := utils.StripAnsi(strings.Join(comp.Render(100), "\n"))
		if strings.Contains(out, "alpha.ts") || !strings.Contains(out, "jkg-plugin.ts") {
			t.Fatalf("expected search filter for jkg, got:\n%s", out)
		}

		// Escape clears search first instead of closing.
		comp.HandleInput("\x1b")
		if comp.GetResourceList().GetSearchQuery() != "" || closed != 0 {
			t.Fatalf("expected first Esc to clear search only, query=%q closed=%d", comp.GetResourceList().GetSearchQuery(), closed)
		}

		// Second Escape closes.
		comp.HandleInput("\x1b")
		if closed != 1 {
			t.Fatalf("expected second Esc to close, got closed=%d", closed)
		}

		// Ctrl+C exits.
		comp.HandleInput("\x03")
		if exited != 1 {
			t.Fatalf("expected Ctrl+C to call onExit, got exited=%d", exited)
		}
	})

	t.Run("groups packages before top-level, respects subgroup order, and toggles global settings", func(t *testing.T) {
		sm := core.SettingsManagerInMemory(&core.Settings{
			Packages: []core.PackageSource{
				core.PackageSourceFromString("npm:demo-pkg"),
			},
		})
		renderedCount := 0
		scoped := ScopedResolvedPaths{
			Global: core.ResolvedPaths{
				Themes: []core.ResolvedResource{
					{
						Path:    "/tmp/agent/themes/ocean.json",
						Enabled: true,
						Metadata: utils.PathMetadata{
							Source:  "local",
							Scope:   utils.SourceScopeUser,
							Origin:  utils.SourceOriginTopLevel,
							BaseDir: "/tmp/agent",
						},
					},
				},
				Skills: []core.ResolvedResource{
					{
						Path:    "/tmp/pkg/skills/deploy/SKILL.md",
						Enabled: true,
						Metadata: utils.PathMetadata{
							Source:  "npm:demo-pkg",
							Scope:   utils.SourceScopeUser,
							Origin:  utils.SourceOriginPackage,
							BaseDir: "/tmp/pkg",
						},
					},
				},
				Extensions: []core.ResolvedResource{
					{
						Path:    "builtin:read",
						Enabled: true,
						Metadata: utils.PathMetadata{
							Source: "npm:demo-pkg",
							Scope:  utils.SourceScopeUser,
							Origin: utils.SourceOriginPackage,
						},
					},
				},
			},
		}

		comp := NewConfigSelectorComponent(
			scoped,
			sm,
			"/tmp/proj",
			"/tmp/agent",
			nil,
			nil,
			func() { renderedCount++ },
			30,
			ConfigWriteScopeGlobal,
			true,
		)

		out := utils.StripAnsi(strings.Join(comp.Render(100), "\n"))
		idxPkg := strings.Index(out, "npm:demo-pkg")
		idxBuiltin := strings.Index(out, "builtin:read")
		idxSkill := strings.Index(out, "deploy (skills)")
		idxLocal := strings.Index(out, "ocean.json (themes)")
		if idxPkg < 0 || idxBuiltin < idxPkg || idxSkill < idxBuiltin || idxLocal < idxSkill {
			t.Fatalf("unexpected group/subgroup ordering:\n%s", out)
		}

		// Toggle first item (builtin:read inside npm:demo-pkg) -> disables it in PackageSourceObject
		comp.HandleInput("\r")
		if renderedCount != 1 {
			t.Fatalf("expected requestRender on toggle, got %d", renderedCount)
		}
		pkgs := sm.GetPackages()
		if len(pkgs) != 1 || pkgs[0].Object == nil || !slices.Contains(pkgs[0].Object.Extensions, "!builtin:read") {
			t.Fatalf("expected !builtin:read in PackageSourceObject, got %+v", pkgs)
		}

		// Toggle again -> re-enables it
		comp.HandleInput("\r")
		pkgs = sm.GetPackages()
		if pkgs[0].Object == nil || !slices.Contains(pkgs[0].Object.Extensions, "builtin:read") {
			t.Fatalf("expected builtin:read re-enabled in PackageSourceObject, got %+v", pkgs)
		}

		// Tab switches to project mode
		comp.HandleInput("\t")
		if comp.GetResourceList().GetWriteScope() != ConfigWriteScopeProject {
			t.Fatalf("expected Tab to switch to project write scope")
		}
		outProj := utils.StripAnsi(strings.Join(comp.Render(100), "\n"))
		if !strings.Contains(outProj, "◉ Project") {
			t.Fatalf("expected project tab active in header, got:\n%s", outProj)
		}
	})

	t.Run("handles nil settings manager and untrusted project toggles without panicking", func(t *testing.T) {
		scoped := ScopedResolvedPaths{
			Project: core.ResolvedPaths{
				Extensions: []core.ResolvedResource{
					{
						Path:    "/tmp/proj/.pi/extensions/custom.ts",
						Enabled: true,
						Metadata: utils.PathMetadata{
							Source:  "project",
							Scope:   utils.SourceScopeProject,
							Origin:  utils.SourceOriginTopLevel,
							BaseDir: "/tmp/proj/.pi",
						},
					},
				},
			},
			Global: core.ResolvedPaths{
				Extensions: []core.ResolvedResource{
					{
						Path:    "/tmp/agent/extensions/local.ts",
						Enabled: true,
						Metadata: utils.PathMetadata{
							Source: "local",
							Scope:  utils.SourceScopeUser,
							Origin: utils.SourceOriginTopLevel,
						},
					},
				},
			},
		}

		// Nil settings manager toggles in memory safely
		compNil := NewConfigSelectorComponent(scoped, nil, "/tmp/proj", "/tmp/agent", nil, nil, nil, 25, ConfigWriteScopeGlobal, false)
		compNil.HandleInput("\r")
		outNil := utils.StripAnsi(strings.Join(compNil.Render(100), "\n"))
		if !strings.Contains(outNil, "[ ] local.ts") {
			t.Fatalf("expected nil SettingsManager toggle to flip visual state, got:\n%s", outNil)
		}

		// Untrusted SettingsManager project toggle surfaces error message rather than panicking
		untrusted := false
		smUntrusted := core.SettingsManagerInMemoryWithOptions(nil, &core.SettingsManagerCreateOptions{
			ProjectTrusted: &untrusted,
		})
		compProj := NewConfigSelectorComponent(scoped, smUntrusted, "/tmp/proj", "/tmp/agent", nil, nil, nil, 25, ConfigWriteScopeProject, true)
		compProj.HandleInput("\r")
		outErr := utils.StripAnsi(strings.Join(compProj.Render(100), "\n"))
		if !strings.Contains(outErr, "not trusted") {
			t.Fatalf("expected project trust error displayed in status line, got:\n%s", outErr)
		}
	})
}
