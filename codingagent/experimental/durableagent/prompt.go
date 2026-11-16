// Ported from packages/coding-agent/src/experimental/durable/prompt.ts (pi v1.0.0).

package durableagent

import (
	"sync"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/durable"
)

type promptResources struct {
	contextFiles []core.ContextFile
	skills       []core.Skill
}

var promptContributions = map[string]core.ToolSystemPromptContribution{
	"read":  core.ReadToolSystemPromptContribution,
	"bash":  core.BashToolSystemPromptContribution,
	"edit":  core.EditToolSystemPromptContribution,
	"write": core.WriteToolSystemPromptContribution,
}

// pi's section order; BuildSystemPromptSections omits the ones without content.
var promptKeys = []string{"preamble", "tools", "rules", "docs", "project_context", "skills", "cwd"}

// CreatePiPrompt returns pi's system prompt as one extension: the sections of
// BuildSystemPromptSections for the request's tools and the conversation's directory.
// Context files and skills load once per directory, like pi at startup.
func CreatePiPrompt(settings *core.SettingsManager, fallbackCwd string) *durable.Extension {
	var mu sync.Mutex
	resources := make(map[string]promptResources)

	load := func(cwd string) promptResources {
		mu.Lock()
		found, ok := resources[cwd]
		mu.Unlock()
		if ok {
			return found
		}
		agentDir := utils.GetAgentDir()
		var skillPaths []string
		if settings != nil {
			skillPaths = settings.GetSkillPaths()
		}
		includeDefaults := true
		loaded := promptResources{
			contextFiles: core.LoadProjectContextFiles(&core.LoadProjectContextFilesOptions{
				Cwd:      cwd,
				AgentDir: agentDir,
			}),
			skills: core.LoadSkills(&core.LoadSkillsOptions{
				Cwd:             cwd,
				AgentDir:        agentDir,
				SkillPaths:      skillPaths,
				IncludeDefaults: &includeDefaults,
			}).Skills,
		}
		mu.Lock()
		if existing, exists := resources[cwd]; exists {
			loaded = existing
		} else {
			resources[cwd] = loaded
		}
		mu.Unlock()
		return loaded
	}

	buildSections := func(input durable.PromptInput) (core.SystemPromptSections, error) {
		cwd := fallbackCwd
		if input.Agent != nil && input.Agent.Cwd != nil && *input.Agent.Cwd != "" {
			cwd = *input.Agent.Cwd
		}
		if input.Env != nil && input.Env.Cwd() != "" {
			cwd = input.Env.Cwd()
		}
		selectedTools := make([]string, 0)
		if input.Agent != nil {
			for _, t := range input.Agent.Tools {
				if t != nil {
					selectedTools = append(selectedTools, t.Name)
				}
			}
		}
		snippets := make(map[string]string)
		guidelines := make(map[string][]string)
		for _, name := range selectedTools {
			contribution, ok := promptContributions[name]
			if !ok {
				continue
			}
			snippets[name] = contribution.Snippet
			guidelines[name] = append([]string(nil), contribution.Guidelines...)
		}
		loaded := load(cwd)
		return core.BuildSystemPromptSections(core.BuildSystemPromptOptions{
			Cwd:            cwd,
			SelectedTools:  selectedTools,
			ToolSnippets:   snippets,
			ToolGuidelines: guidelines,
			ContextFiles:   loaded.contextFiles,
			Skills:         loaded.skills,
		})
	}

	sections := make([]*durable.PromptSection, 0, len(promptKeys))
	for _, key := range promptKeys {
		sectionKey := key
		// The built sections carry their own tags.
		sections = append(sections, durable.Section(sectionKey, func(input durable.PromptInput, _ chord.Context) (string, bool, error) {
			built, err := buildSections(input)
			if err != nil {
				return "", false, err
			}
			text, ok := built.Get(sectionKey)
			if !ok || text == "" {
				return "", false, nil
			}
			return text, true, nil
		}, false))
	}

	return durable.DefineExtension(&durable.Extension{
		Name:     "pi-prompt",
		Sections: sections,
	})
}
