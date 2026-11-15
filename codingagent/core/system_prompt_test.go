package core

import (
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/omap"
)

func tspSampleSkill() Skill {
	return Skill{
		Name:        "test-skill",
		Description: "A test skill.",
		FilePath:    "/tmp/test-skill/SKILL.md",
		BaseDir:     "/tmp/test-skill",
		SourceInfo:  utils.SourceInfo{Source: "user"},
	}
}

func TestBuildSystemPromptDefaultEmptyToolsAndAddendum(t *testing.T) {
	prompt, err := BuildSystemPrompt(BuildSystemPromptOptions{
		SelectedTools: []string{},
		Cwd:           "/tmp/work",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "<tools>\n(none)\n\nAdditional tools may be available.\n</tools>") {
		t.Fatalf("expected empty tools section, got:\n%s", prompt)
	}
	if strings.Contains(prompt, "- read:") || strings.Contains(prompt, "- bash:") {
		t.Fatalf("unexpected built-in tool descriptions in empty-tools prompt:\n%s", prompt)
	}

	addendum := "Always write unit tests."
	withAddendum, err := BuildSystemPrompt(BuildSystemPromptOptions{
		AppendSystemPrompt: &addendum,
		ContextFiles:       []ContextFile{{Path: "/tmp/work/AGENTS.md", Content: "Use gofmt."}},
		Cwd:                "/tmp/work",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(withAddendum, "Always write unit tests.") {
		t.Fatalf("missing appended system prompt in:\n%s", withAddendum)
	}
	if !strings.Contains(withAddendum, `<project_instructions path="/tmp/work/AGENTS.md">`) || !strings.Contains(withAddendum, "Use gofmt.") {
		t.Fatalf("missing project_instructions block in:\n%s", withAddendum)
	}
}

func TestBuildSystemPromptShellGuidanceDocsAndCustomSnippets(t *testing.T) {
	shellCases := []struct {
		tools    []string
		expected string
	}{
		{[]string{"bash"}, "Use bash for file operations"},
		{[]string{"powershell"}, "Use PowerShell for file operations"},
		{[]string{"bash", "powershell"}, "Use bash or PowerShell for file operations"},
	}
	for _, tc := range shellCases {
		prompt, err := BuildSystemPrompt(BuildSystemPromptOptions{
			SelectedTools: tc.tools,
			Cwd:           "/tmp/work",
		})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(prompt, tc.expected) {
			t.Fatalf("expected %q for tools=%v, got:\n%s", tc.expected, tc.tools, prompt)
		}
	}

	defaultPrompt, err := BuildSystemPrompt(BuildSystemPromptOptions{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"- When reading pi docs or examples, resolve docs/... under Additional docs and examples/... under Examples, not the current working directory",
		"environment variables (docs/environment-variables.md), MCP servers (docs/mcp.md)",
		"Before implementing harness changes, read the relevant docs and examples in full, following related cross-references.",
	} {
		if !strings.Contains(defaultPrompt, expected) {
			t.Fatalf("missing doc instruction %q in default prompt", expected)
		}
	}

	withSnippet, err := BuildSystemPrompt(BuildSystemPromptOptions{
		SelectedTools: []string{"read", "dynamic_tool"},
		ToolSnippets:  map[string]string{"dynamic_tool": "Run dynamic test behavior"},
		Cwd:           "/tmp/work",
	})
	if err != nil || !strings.Contains(withSnippet, "- dynamic_tool: Run dynamic test behavior") {
		t.Fatalf("expected dynamic_tool snippet, err=%v prompt:\n%s", err, withSnippet)
	}

	withoutSnippet, err := BuildSystemPrompt(BuildSystemPromptOptions{
		SelectedTools: []string{"read", "dynamic_tool"},
		Cwd:           "/tmp/work",
	})
	if err != nil || strings.Contains(withoutSnippet, "dynamic_tool") {
		t.Fatalf("expected dynamic_tool omitted without snippet, err=%v", err)
	}
}

func TestBuildSystemPromptGuidelinesSkillsAndHiddenTools(t *testing.T) {
	dedupPrompt, err := BuildSystemPrompt(BuildSystemPromptOptions{
		SelectedTools: []string{"read", "dynamic_tool"},
		PromptGuidelines: []string{
			"Use dynamic_tool for summaries.",
			"  Use dynamic_tool for summaries.  ",
			"   ",
		},
		Cwd: "/tmp/work",
	})
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(dedupPrompt, "- Use dynamic_tool for summaries."); count != 1 {
		t.Fatalf("expected deduplicated prompt guideline count=1, got %d", count)
	}

	skill := tspSampleSkill()
	custom := "Custom system prompt"
	for _, cp := range []*string{nil, &custom} {
		p, err := BuildSystemPrompt(BuildSystemPromptOptions{
			CustomPrompt:  cp,
			SelectedTools: []string{"bash"},
			Skills:        []Skill{skill},
			Cwd:           "/tmp/work",
		})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(p, "<skills>") || !strings.Contains(p, "<available_skills>") || !strings.Contains(p, "<name>test-skill</name>") || !strings.Contains(p, "Use bash to load a skill's file") {
			t.Fatalf("expected bash skills block in prompt:\n%s", p)
		}
	}

	noReaderPrompt, err := BuildSystemPrompt(BuildSystemPromptOptions{
		SelectedTools: []string{"write"},
		Skills:        []Skill{skill},
		Cwd:           "/tmp/work",
	})
	if err != nil || strings.Contains(noReaderPrompt, "<available_skills>") {
		t.Fatalf("expected skills omitted without read/bash, err=%v", err)
	}

	buildHidden := func(hidden []string) string {
		p, err := BuildSystemPrompt(BuildSystemPromptOptions{
			SelectedTools: []string{"read", "bash", "run"},
			HiddenTools:   hidden,
			ToolSnippets: map[string]string{
				"read": "Read files",
				"bash": "Run commands",
				"run":  "Run a task",
			},
			ToolGuidelines: map[string][]string{
				"read": {"Use read for files."},
				"run":  {"Prefer run."},
			},
			Skills: []Skill{skill},
			Cwd:    "/tmp/work",
		})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}

	bothHidden := buildHidden([]string{"read", "bash"})
	if !strings.Contains(bothHidden, "<tools>\n- run: Run a task\n") ||
		strings.Contains(bothHidden, "- read: ") ||
		strings.Contains(bothHidden, "Use read for files.") ||
		strings.Contains(bothHidden, "Use bash for file operations") ||
		!strings.Contains(bothHidden, "- Prefer run.") {
		t.Fatalf("unexpected hidden tools filtering:\n%s", bothHidden)
	}
	if !strings.Contains(bothHidden, "\nLoad a skill's file when the task matches its description.") {
		t.Fatalf("expected generic skills instruction when read+bash hidden:\n%s", bothHidden)
	}
	if !strings.Contains(buildHidden([]string{"read"}), "Use bash to load a skill's file") {
		t.Fatal("expected bash skill fallback when read hidden")
	}
	if !strings.Contains(buildHidden(nil), "Use the read tool to load a skill's file") {
		t.Fatal("expected read skill wording when nothing hidden")
	}

	// Custom sections order preservation check.
	secs := omap.NewMap[string, string]()
	secs.Set("plan_mode", "Plan only.")
	secPrompt, err := BuildSystemPrompt(BuildSystemPromptOptions{
		Sections: secs,
		Cwd:      "/tmp/work",
	})
	if err != nil || !strings.Contains(secPrompt, "<plan_mode>\nPlan only.\n</plan_mode>") {
		t.Fatalf("expected wrapped custom section, err=%v prompt:\n%s", err, secPrompt)
	}
}
