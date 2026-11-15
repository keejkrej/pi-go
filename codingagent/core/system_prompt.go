// Ported from packages/coding-agent/src/core/system-prompt.ts (pi v1.5.2).

package core

import (
	"fmt"
	"slices"
	"strings"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsre"
	"github.com/keejkrej/pi-go/internal/omap"
)

// ContextFile holds a project context file path and markdown content (e.g., AGENTS.md).
type ContextFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// BuildSystemPromptOptions configures structured system prompt construction.
type BuildSystemPromptOptions struct {
	// CustomPrompt replaces the default preamble/tools/rules/docs sections.
	CustomPrompt *string `json:"customPrompt,omitzero"`
	// ForceSystemPrompt replaces the entire prompt with an opaque string and omits sections.
	ForceSystemPrompt *string `json:"forceSystemPrompt,omitzero"`
	// SelectedTools lists tools included in the prompt. Nil defaults to ["read", "bash", "edit", "write"].
	SelectedTools []string `json:"selectedTools,omitzero"`
	// HiddenTools lists selected tools omitted from tool declarations.
	HiddenTools []string `json:"hiddenTools,omitzero"`
	// ToolSnippets maps tool names to one-line descriptions.
	ToolSnippets map[string]string `json:"toolSnippets,omitzero"`
	// ToolGuidelines maps tool names to prompt guideline bullets.
	ToolGuidelines map[string][]string `json:"toolGuidelines,omitzero"`
	// PromptGuidelines appends additional guideline bullets to rules.
	PromptGuidelines []string `json:"promptGuidelines,omitzero"`
	// AppendSystemPrompt appends user-configured addendum text.
	AppendSystemPrompt *string `json:"appendSystemPrompt,omitzero"`
	// Sections supplies additional XML-wrapped sections keyed by tag name in insertion order.
	Sections *omap.Map[string, string] `json:"sections,omitzero"`
	// Cwd is the working directory rendered into the <cwd> section.
	Cwd string `json:"cwd"`
	// ContextFiles holds pre-loaded project context files.
	ContextFiles []ContextFile `json:"contextFiles,omitzero"`
	// Skills holds pre-loaded skills.
	Skills []Skill `json:"skills,omitzero"`
}

// NormalizedBuildSystemPromptOptions is the mutable, collection-complete shape exposed to extensions.
type NormalizedBuildSystemPromptOptions struct {
	CustomPrompt       *string                   `json:"customPrompt,omitzero"`
	ForceSystemPrompt  *string                   `json:"forceSystemPrompt,omitzero"`
	SelectedTools      []string                  `json:"selectedTools"`
	HiddenTools        []string                  `json:"hiddenTools"`
	ToolSnippets       map[string]string         `json:"toolSnippets"`
	ToolGuidelines     map[string][]string       `json:"toolGuidelines"`
	PromptGuidelines   []string                  `json:"promptGuidelines"`
	AppendSystemPrompt string                    `json:"appendSystemPrompt"`
	Sections           *omap.Map[string, string] `json:"sections"`
	Cwd                string                    `json:"cwd"`
	ContextFiles       []ContextFile             `json:"contextFiles"`
	Skills             []Skill                   `json:"skills"`
}

// SystemPromptSections holds ordered prompt sections keyed by section name.
type SystemPromptSections = *omap.Map[string, string]

// SystemPromptState holds the rendered prompt string or structured sections for SystemMessage.
type SystemPromptState struct {
	Content  string               `json:"content"`
	Sections SystemPromptSections `json:"sections,omitzero"`
}

var spSectionNameRe = jsre.MustCompile(`^[a-z][a-z0-9_-]*$`, "")

// NormalizeBuildSystemPromptOptions copies input into a mutable, non-nil collection form.
func NormalizeBuildSystemPromptOptions(input BuildSystemPromptOptions) NormalizedBuildSystemPromptOptions {
	selectedTools := []string{"read", "bash", "edit", "write"}
	if input.SelectedTools != nil {
		selectedTools = slices.Clone(input.SelectedTools)
	}
	hiddenTools := []string{}
	if input.HiddenTools != nil {
		hiddenTools = slices.Clone(input.HiddenTools)
	}
	toolSnippets := make(map[string]string, len(input.ToolSnippets))
	for k, v := range input.ToolSnippets {
		toolSnippets[k] = v
	}
	toolGuidelines := make(map[string][]string, len(input.ToolGuidelines))
	for k, v := range input.ToolGuidelines {
		toolGuidelines[k] = slices.Clone(v)
	}
	promptGuidelines := []string{}
	if input.PromptGuidelines != nil {
		promptGuidelines = slices.Clone(input.PromptGuidelines)
	}
	appendPrompt := ""
	if input.AppendSystemPrompt != nil {
		appendPrompt = *input.AppendSystemPrompt
	}
	sections := omap.NewMap[string, string]()
	if input.Sections != nil {
		for k, v := range input.Sections.All() {
			sections.Set(k, v)
		}
	}
	contextFiles := make([]ContextFile, len(input.ContextFiles))
	copy(contextFiles, input.ContextFiles)
	skills := make([]Skill, len(input.Skills))
	copy(skills, input.Skills)

	return NormalizedBuildSystemPromptOptions{
		CustomPrompt:       input.CustomPrompt,
		ForceSystemPrompt:  input.ForceSystemPrompt,
		SelectedTools:      selectedTools,
		HiddenTools:        hiddenTools,
		ToolSnippets:       toolSnippets,
		ToolGuidelines:     toolGuidelines,
		PromptGuidelines:   promptGuidelines,
		AppendSystemPrompt: appendPrompt,
		Sections:           sections,
		Cwd:                input.Cwd,
		ContextFiles:       contextFiles,
		Skills:             skills,
	}
}

func spRenderProjectContext(contextFiles []ContextFile) string {
	parts := make([]string, 0, len(contextFiles)+1)
	parts = append(parts, "Project-specific instructions and guidelines:")
	for _, f := range contextFiles {
		parts = append(parts, fmt.Sprintf("<project_instructions path=\"%s\">\n%s\n</project_instructions>", f.Path, f.Content))
	}
	return strings.Join(parts, "\n\n")
}

func spBuildRules(selectedTools []string, toolGuidelines map[string][]string, promptGuidelines []string) string {
	var rules []string
	seen := make(map[string]struct{})
	addRule := func(rule string) {
		normalized := strings.TrimSpace(rule)
		if normalized == "" {
			return
		}
		if _, ok := seen[normalized]; ok {
			return
		}
		seen[normalized] = struct{}{}
		rules = append(rules, normalized)
	}

	hasBash := slices.Contains(selectedTools, "bash")
	hasPowerShell := slices.Contains(selectedTools, "powershell")
	hasGrep := slices.Contains(selectedTools, "grep")
	hasFind := slices.Contains(selectedTools, "find")
	hasLs := slices.Contains(selectedTools, "ls")

	if (hasBash || hasPowerShell) && !hasGrep && !hasFind && !hasLs {
		if hasBash && hasPowerShell {
			addRule("Use bash or PowerShell for file operations like listing, searching, and finding files")
		} else if hasPowerShell {
			addRule("Use PowerShell for file operations like listing, searching, and finding files")
		} else {
			addRule("Use bash for file operations like ls, rg, find")
		}
	}

	for _, name := range selectedTools {
		for _, rule := range toolGuidelines[name] {
			addRule(rule)
		}
	}
	for _, rule := range promptGuidelines {
		addRule(rule)
	}
	addRule("Be concise in your responses")
	addRule("Show file paths clearly when working with files")

	lines := make([]string, len(rules))
	for i, rule := range rules {
		lines[i] = "- " + rule
	}
	return strings.Join(lines, "\n")
}

// BuildSystemPromptSections builds the ordered, independently replaceable sections of the system prompt.
func BuildSystemPromptSections(input BuildSystemPromptOptions) (SystemPromptSections, error) {
	opts := NormalizeBuildSystemPromptOptions(input)

	for _, name := range opts.Sections.Keys() {
		if !spSectionNameRe.Test(name) || name == "preamble" {
			return nil, fmt.Errorf("Invalid system prompt section name: %s", name)
		}
	}

	declaredTools := make([]string, 0, len(opts.SelectedTools))
	for _, name := range opts.SelectedTools {
		if !slices.Contains(opts.HiddenTools, name) {
			declaredTools = append(declaredTools, name)
		}
	}

	promptSections := omap.NewMap[string, string]()
	if opts.CustomPrompt != nil && *opts.CustomPrompt != "" {
		promptSections.Set("preamble", *opts.CustomPrompt)
	} else {
		promptSections.Set(
			"preamble",
			"You are a coding assistant running inside pi, a coding agent harness. Help users inspect files, run commands, and modify code.",
		)
		var visibleLines []string
		for _, name := range declaredTools {
			if snippet := opts.ToolSnippets[name]; snippet != "" {
				visibleLines = append(visibleLines, fmt.Sprintf("- %s: %s", name, snippet))
			}
		}
		toolsText := "(none)"
		if len(visibleLines) > 0 {
			toolsText = strings.Join(visibleLines, "\n")
		}
		promptSections.Set("tools", toolsText+"\n\nAdditional tools may be available.")
		promptSections.Set("rules", spBuildRules(declaredTools, opts.ToolGuidelines, opts.PromptGuidelines))
		promptSections.Set("docs", fmt.Sprintf(`Pi documentation (read only when the user asks about the harness itself, its SDK, extensions, themes, skills, or TUI):
- Main documentation: %s
- Additional docs: %s
- Examples: %s (extensions, custom tools, SDK)
- When reading pi docs or examples, resolve docs/... under Additional docs and examples/... under Examples, not the current working directory
- When asked about: extensions (docs/extensions.md, examples/extensions/), themes (docs/themes.md), skills (docs/skills.md), prompt templates (docs/prompt-templates.md), TUI components (docs/tui.md), keybindings (docs/keybindings.md), SDK integrations (docs/sdk.md), custom providers (docs/custom-provider.md), adding models (docs/models.md), packages (docs/packages.md), environment variables (docs/environment-variables.md), MCP servers (docs/mcp.md), codemode scripts and non-LLM models such as classifiers and image models (docs/codemode.md)
- Before implementing harness changes, read the relevant docs and examples in full, following related cross-references.`, utils.GetReadmePath(), utils.GetDocsPath(), utils.GetExamplesPath()))
	}

	if opts.AppendSystemPrompt != "" {
		promptSections.Set("addendum", opts.AppendSystemPrompt)
	}
	if len(opts.ContextFiles) > 0 {
		promptSections.Set("project_context", spRenderProjectContext(opts.ContextFiles))
	}

	var skillFileReadTool string
	for _, reader := range []string{"read", "bash"} {
		if slices.Contains(declaredTools, reader) {
			skillFileReadTool = reader
			break
		}
	}
	if skillFileReadTool == "" {
		if slices.Contains(opts.SelectedTools, "read") || slices.Contains(opts.SelectedTools, "bash") {
			skillFileReadTool = "indirect"
		}
	}
	if skillFileReadTool != "" && len(opts.Skills) > 0 {
		if skillsPrompt := strings.TrimSpace(FormatSkillsForPrompt(opts.Skills, &FormatSkillsForPromptOptions{Method: skillFileReadTool})); skillsPrompt != "" {
			promptSections.Set("skills", skillsPrompt)
		}
	}

	promptSections.Set("cwd", strings.ReplaceAll(opts.Cwd, `\`, "/"))
	for name, content := range opts.Sections.All() {
		if content != "" {
			promptSections.Set(name, content)
		}
	}

	sections := omap.NewMap[string, string]()
	preamble, _ := promptSections.Get("preamble")
	sections.Set("preamble", preamble)
	for name, content := range promptSections.All() {
		if name != "preamble" {
			sections.Set(name, fmt.Sprintf("<%s>\n%s\n</%s>", name, content, name))
		}
	}
	return sections, nil
}

// BuildSystemPromptState builds the SystemPromptState for input.
func BuildSystemPromptState(input BuildSystemPromptOptions) (SystemPromptState, error) {
	if input.ForceSystemPrompt != nil {
		return SystemPromptState{Content: *input.ForceSystemPrompt}, nil
	}
	sections, err := BuildSystemPromptSections(input)
	if err != nil {
		return SystemPromptState{}, err
	}
	return SystemPromptState{Content: "", Sections: sections}, nil
}

// BuildSystemPrompt renders the system prompt identically to ai.GetSystemMessageText.
func BuildSystemPrompt(input BuildSystemPromptOptions) (string, error) {
	state, err := BuildSystemPromptState(input)
	if err != nil {
		return "", err
	}
	empty := state.Content
	var ptrSections *omap.Map[string, *string]
	if state.Sections != nil {
		ptrSections = omap.NewMap[string, *string]()
		for k, v := range state.Sections.All() {
			val := v
			ptrSections.Set(k, &val)
		}
	}
	return ai.GetSystemMessageText(&ai.SystemMessage{
		Role:     "system",
		Content:  ai.SystemMessageContent{Text: &empty},
		Sections: ptrSections,
	}), nil
}

// DiffSystemPromptSections returns an *omap.Map patch mapping changed or removed sections to new text or nil.
func DiffSystemPromptSections(previous *omap.Map[string, *string], current SystemPromptSections) *omap.Map[string, *string] {
	patch := omap.NewMap[string, *string]()
	if current != nil {
		for name, text := range current.All() {
			var prevVal *string
			if previous != nil {
				prevVal, _ = previous.Get(name)
			}
			if prevVal == nil || *prevVal != text {
				t := text
				patch.Set(name, &t)
			}
		}
	}
	if previous != nil {
		for _, name := range previous.Keys() {
			if current == nil {
				patch.Set(name, nil)
				continue
			}
			if _, ok := current.Get(name); !ok {
				patch.Set(name, nil)
			}
		}
	}
	if patch.Len() == 0 {
		return nil
	}
	return patch
}
