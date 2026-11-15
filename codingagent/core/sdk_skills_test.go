// Ported from packages/coding-agent/test/sdk-skills.test.ts (pi v1.0.0).

package core_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/internal/testkit"
	"github.com/keejkrej/pi-go/codingagent/utils"
)

func tssSetupDir(t *testing.T) string {
	t.Helper()
	tempDir := t.TempDir()
	skillsDir := filepath.Join(tempDir, "skills", "test-skill")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	skillContent := "---\nname: test-skill\ndescription: A test skill for SDK tests.\n---\n\n# Test Skill\n\nThis is a test skill.\n"
	if err := os.WriteFile(filepath.Join(skillsDir, "SKILL.md"), []byte(skillContent), 0o644); err != nil {
		t.Fatal(err)
	}
	return tempDir
}

func TestSdkSkills(t *testing.T) {
	ctx := context.Background()

	t.Run("discovers skills by default and exposes them on resource loader", func(t *testing.T) {
		tempDir := tssSetupDir(t)
		res, err := core.CreateAgentSession(ctx, &core.CreateAgentSessionOptions{
			Cwd:            tempDir,
			AgentDir:       tempDir,
			SessionManager: core.SessionManagerInMemory(),
		})
		if err != nil {
			t.Fatalf("CreateAgentSession: %v", err)
		}
		defer res.Session.Dispose()

		skills := res.Session.ResourceLoader().GetSkills().Skills
		if len(skills) == 0 {
			t.Fatal("expected discovered skills > 0")
		}
		found := false
		for _, sk := range skills {
			if sk.Name == "test-skill" {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected test-skill in %#v", skills)
		}
	})

	t.Run("has empty skills when resource loader returns none", func(t *testing.T) {
		tempDir := tssSetupDir(t)
		rl := testkit.CreateTestResourceLoader()

		res, err := core.CreateAgentSession(ctx, &core.CreateAgentSessionOptions{
			Cwd:            tempDir,
			AgentDir:       tempDir,
			SessionManager: core.SessionManagerInMemory(),
			ResourceLoader: rl,
		})
		if err != nil {
			t.Fatalf("CreateAgentSession: %v", err)
		}
		defer res.Session.Dispose()

		loaded := res.Session.ResourceLoader().GetSkills()
		if len(loaded.Skills) != 0 {
			t.Fatalf("expected empty skills, got %#v", loaded.Skills)
		}
		if len(loaded.Diagnostics) != 0 {
			t.Fatalf("expected empty diagnostics, got %#v", loaded.Diagnostics)
		}
	})

	t.Run("uses provided skills when resource loader supplies them", func(t *testing.T) {
		tempDir := tssSetupDir(t)
		customSkill := core.Skill{
			Name:        "custom-skill",
			Description: "A custom skill",
			FilePath:    "/fake/path/SKILL.md",
			BaseDir:     "/fake/path",
			SourceInfo: utils.CreateSyntheticSourceInfo("/fake/path/SKILL.md", utils.SyntheticSourceOptions{
				Source: "sdk",
			}),
			DisableModelInvocation: false,
		}
		rl := testkit.CreateTestResourceLoader(&testkit.CreateTestResourceLoaderOptions{
			Skills: []core.Skill{customSkill},
		})

		res, err := core.CreateAgentSession(ctx, &core.CreateAgentSessionOptions{
			Cwd:            tempDir,
			AgentDir:       tempDir,
			SessionManager: core.SessionManagerInMemory(),
			ResourceLoader: rl,
		})
		if err != nil {
			t.Fatalf("CreateAgentSession: %v", err)
		}
		defer res.Session.Dispose()

		loaded := res.Session.ResourceLoader().GetSkills()
		if !reflect.DeepEqual(loaded.Skills, []core.Skill{customSkill}) {
			t.Fatalf("Skills = %#v, want %#v", loaded.Skills, []core.Skill{customSkill})
		}
		if len(loaded.Diagnostics) != 0 {
			t.Fatalf("expected empty diagnostics, got %#v", loaded.Diagnostics)
		}
	})
}
