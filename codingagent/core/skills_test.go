package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/utils"
)

func tskillsBoolPtr(v bool) *bool { return &v }

func TestLoadSkillsFromDirValidAndCollisions(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "alpha-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	skillFile := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillFile, []byte("---\nname: alpha-skill\ndescription: First alpha skill.\ndisable-model-invocation: true\n---\n\n# Alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Root-level markdown file directly inside dir.
	rootSkill := filepath.Join(root, "root-skill.md")
	if err := os.WriteFile(rootSkill, []byte("---\nname: root-skill\ndescription: Root markdown skill.\n---\nBody\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := LoadSkillsFromDir(LoadSkillsFromDirOptions{
		Dir:              root,
		Source:           "local",
		Scope:            utils.SourceScopeProject,
		Origin:           utils.SourceOriginTopLevel,
		IncludeRootFiles: tskillsBoolPtr(true),
	})
	if len(res.Diagnostics) != 0 {
		t.Fatalf("expected 0 diagnostics, got %+v", res.Diagnostics)
	}
	if len(res.Skills) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(res.Skills))
	}

	byName := map[string]Skill{}
	for _, s := range res.Skills {
		byName[s.Name] = s
	}
	alpha, ok := byName["alpha-skill"]
	if !ok {
		t.Fatalf("missing alpha-skill in %+v", byName)
	}
	if alpha.Description != "First alpha skill." || !alpha.DisableModelInvocation {
		t.Fatalf("unexpected alpha skill metadata: %+v", alpha)
	}
	if alpha.BaseDir != skillDir || alpha.FilePath != skillFile {
		t.Fatalf("unexpected paths: baseDir=%q filePath=%q", alpha.BaseDir, alpha.FilePath)
	}
	if _, ok := byName["root-skill"]; !ok {
		t.Fatalf("missing root-skill in %+v", byName)
	}

	// IncludeRootFiles=false ignores root markdown files, keeps subdirectory SKILL.md.
	resNoRoot := LoadSkillsFromDir(LoadSkillsFromDirOptions{
		Dir:              root,
		Source:           "local",
		Scope:            utils.SourceScopeProject,
		Origin:           utils.SourceOriginTopLevel,
		IncludeRootFiles: tskillsBoolPtr(false),
	})
	if len(resNoRoot.Skills) != 1 || resNoRoot.Skills[0].Name != "alpha-skill" {
		t.Fatalf("expected only alpha-skill when IncludeRootFiles=false, got %+v", resNoRoot.Skills)
	}
}

func TestLoadSkillsValidationDiagnosticsAndIgnore(t *testing.T) {
	root := t.TempDir()

	// Missing description -> warning diagnostic, not loaded.
	badDescDir := filepath.Join(root, "missing-desc")
	if err := os.MkdirAll(badDescDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badDescDir, "SKILL.md"), []byte("---\nname: missing-desc\n---\nNo desc\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Consecutive hyphens -> warning diagnostic AND still loaded under frontmatter name.
	hyphenDir := filepath.Join(root, "bad--name")
	if err := os.MkdirAll(hyphenDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hyphenDir, "SKILL.md"), []byte("---\nname: bad--name\ndescription: Valid desc.\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Uppercase invalid chars -> warning diagnostic AND still loads cleanly unless description missing.
	upperDir := filepath.Join(root, "BadCase")
	if err := os.MkdirAll(upperDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(upperDir, "SKILL.md"), []byte("---\nname: BadCase\ndescription: Bad case desc.\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Ignored by .gitignore.
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("ignored-dir/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ignoredDir := filepath.Join(root, "ignored-dir")
	if err := os.MkdirAll(ignoredDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ignoredDir, "SKILL.md"), []byte("---\nname: ignored-dir\ndescription: Should be ignored.\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := LoadSkillsFromDir(LoadSkillsFromDirOptions{
		Dir:    root,
		Source: "local",
		Scope:  utils.SourceScopeProject,
		Origin: utils.SourceOriginTopLevel,
	})
	if len(res.Skills) != 2 {
		t.Fatalf("expected 2 skills (bad--name, BadCase), got %+v", res.Skills)
	}
	for _, s := range res.Skills {
		if s.Name == "ignored-dir" {
			t.Fatalf("ignored-dir should have been skipped via .gitignore")
		}
	}
	if len(res.Diagnostics) < 3 {
		t.Fatalf("expected diagnostics for missing description, consecutive hyphens, and invalid chars, got %+v", res.Diagnostics)
	}
}

func TestLoadSkillsPrecedenceProjectTrustAndCollisions(t *testing.T) {
	base := t.TempDir()
	t.Setenv("HOME", base)
	cwd := filepath.Join(base, "repo")
	agentDir := filepath.Join(base, "agent")

	projectSkillDir := filepath.Join(cwd, utils.ConfigDirName, "skills", "shared-skill")
	userSkillDir := filepath.Join(agentDir, "skills", "shared-skill")
	explicitFile := filepath.Join(base, "explicit", "extra-skill.md")
	for _, d := range []string{projectSkillDir, userSkillDir, filepath.Dir(explicitFile)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(projectSkillDir, "SKILL.md"), []byte("---\nname: shared-skill\ndescription: From project\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userSkillDir, "SKILL.md"), []byte("---\nname: shared-skill\ndescription: From user\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(explicitFile, []byte("---\nname: extra-skill\ndescription: Explicit single file\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	missingExplicit := filepath.Join(base, "does-not-exist", "missing.md")

	// When projectTrusted=false, project skills are excluded while user skills load.
	untrusted := LoadSkills(&LoadSkillsOptions{
		Cwd:             cwd,
		AgentDir:        agentDir,
		IncludeDefaults: tskillsBoolPtr(true),
		ProjectTrusted:  tskillsBoolPtr(false),
	})
	if len(untrusted.Skills) != 1 || untrusted.Skills[0].Description != "From user" {
		t.Fatalf("expected user skill when untrusted, got %+v", untrusted.Skills)
	}

	// When projectTrusted=true, project wins over user AND reports a collision diagnostic.
	trusted := LoadSkills(&LoadSkillsOptions{
		Cwd:             cwd,
		AgentDir:        agentDir,
		SkillPaths:      []string{explicitFile, missingExplicit},
		IncludeDefaults: tskillsBoolPtr(true),
		ProjectTrusted:  tskillsBoolPtr(true),
	})
	if len(trusted.Skills) != 2 {
		t.Fatalf("expected 2 skills (extra-skill + shared-skill), got %+v", trusted.Skills)
	}
	byName := map[string]Skill{}
	for _, s := range trusted.Skills {
		byName[s.Name] = s
	}
	if byName["shared-skill"].Description != "From project" {
		t.Fatalf("expected project skill to win collision, got %+v", byName["shared-skill"])
	}
	var sawCollision, sawMissingWarn bool
	for _, d := range trusted.Diagnostics {
		if d.Type == ResourceDiagnosticTypeCollision && d.Collision != nil && d.Collision.Name == "shared-skill" {
			sawCollision = true
		}
		if d.Type == ResourceDiagnosticTypeWarning && d.Path != nil && *d.Path == missingExplicit {
			sawMissingWarn = true
		}
	}
	if !sawCollision {
		t.Fatalf("expected collision diagnostic for shared-skill, got %+v", trusted.Diagnostics)
	}
	if !sawMissingWarn {
		t.Fatalf("expected missing explicit path warning, got %+v", trusted.Diagnostics)
	}
}

func TestFormatSkillsForPrompt(t *testing.T) {
	if got := FormatSkillsForPrompt(nil); got != "" {
		t.Fatalf("expected empty prompt for no skills, got %q", got)
	}
	skills := []Skill{
		{
			Name:        "alpha",
			Description: `Handles <xml> & "quotes"`,
			FilePath:    "/tmp/alpha/SKILL.md",
		},
		{
			Name:                   "hidden",
			Description:            "Disabled from model invocation",
			FilePath:               "/tmp/hidden/SKILL.md",
			DisableModelInvocation: true,
		},
	}

	if got := FormatSkillsForPrompt([]Skill{skills[1]}); got != "" {
		t.Fatalf("expected empty prompt when all skills have disableModelInvocation, got %q", got)
	}

	out := FormatSkillsForPrompt(skills, &FormatSkillsForPromptOptions{Method: "bash"})
	for _, want := range []string{
		"<available_skills>",
		"<name>alpha</name>",
		"Handles &lt;xml&gt; &amp; &quot;quotes&quot;",
		"<location>/tmp/alpha/SKILL.md</location>",
		"Use bash to load a skill's file when the task matches its description.",
		"Resolve relative paths against the skill's directory (parent of SKILL.md). Use absolute paths in tool calls.",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("formatted prompt missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "hidden") {
		t.Fatalf("hidden skill should not appear in prompt:\n%s", out)
	}
}
