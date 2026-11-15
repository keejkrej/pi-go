// Ported from packages/coding-agent/src/experimental/durable/prompt.ts (pi v1.0.0).

package durableagent

import (
	"fmt"
	"strings"
	"sync"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/durable"
)

// DurablePromptExtensionName identifies the coding-agent system prompt extension.
const DurablePromptExtensionName = "pi.durable-prompt"

// DurablePromptExtensionOptions configures CreateDurablePromptExtension.
type DurablePromptExtensionOptions struct {
	Cwd        string
	AgentDir   string
	SkillPaths []string
}

type promptCacheKey struct {
	cwd        string
	agentDir   string
	toolList   string
	appendText string
}

// CreateDurablePromptExtension builds the durable extension that renders coding-agent prompt sections.
func CreateDurablePromptExtension(options DurablePromptExtensionOptions) *durable.Extension {
	var mu sync.Mutex
	contextFilesCache := make(map[string][]core.ContextFile)
	skillsCache := make(map[string][]core.Skill)

	loadResources := func(cwd, agentDir string) ([]core.ContextFile, []core.Skill) {
		key := cwd + "\x00" + agentDir
		mu.Lock()
		cf, hasCf := contextFilesCache[key]
		sk, hasSk := skillsCache[key]
		mu.Unlock()
		if hasCf && hasSk {
			return cf, sk
		}
		loadedCf := core.LoadProjectContextFiles(&core.LoadProjectContextFilesOptions{
			Cwd:      cwd,
			AgentDir: agentDir,
		})
		skillsRes := core.LoadSkills(&core.LoadSkillsOptions{
			Cwd:        cwd,
			AgentDir:   agentDir,
			SkillPaths: options.SkillPaths,
		})
		mu.Lock()
		contextFilesCache[key] = loadedCf
		skillsCache[key] = skillsRes.Skills
		mu.Unlock()
		return loadedCf, skillsRes.Skills
	}

	renderSection := func(sectionKey string) durable.PromptRenderFn {
		return func(input durable.PromptInput, _ chord.Context) (string, bool, error) {
			cwd := options.Cwd
			if input.Agent != nil && input.Agent.Cwd != nil && strings.TrimSpace(*input.Agent.Cwd) != "" {
				cwd = *input.Agent.Cwd
			}
			if cwd == "" {
				cwd = "."
			}
			agentDir := options.AgentDir

			toolNames := make([]string, 0)
			if input.Agent != nil {
				for _, t := range input.Agent.Tools {
					if t != nil {
						toolNames = append(toolNames, t.Name)
					}
				}
			}

			toolSnippets := make(map[string]string)
			toolGuidelines := make(map[string][]string)
			for _, name := range toolNames {
				if contrib, ok := core.BuiltinToolSystemPromptContributions[core.ToolName(name)]; ok {
					if contrib.Snippet != "" {
						toolSnippets[name] = contrib.Snippet
					}
					if len(contrib.Guidelines) > 0 {
						toolGuidelines[name] = contrib.Guidelines
					}
				}
			}

			cf, sk := loadResources(cwd, agentDir)
			var appendPrompt *string
			if input.Agent != nil && input.Agent.Instructions != nil && strings.TrimSpace(*input.Agent.Instructions) != "" {
				appendPrompt = input.Agent.Instructions
			}

			sections, err := core.BuildSystemPromptSections(core.BuildSystemPromptOptions{
				SelectedTools:      toolNames,
				ToolSnippets:       toolSnippets,
				ToolGuidelines:     toolGuidelines,
				AppendSystemPrompt: appendPrompt,
				Cwd:                cwd,
				ContextFiles:       cf,
				Skills:             sk,
			})
			if err != nil {
				return "", false, err
			}
			text, ok := sections.Get(sectionKey)
			if !ok || strings.TrimSpace(text) == "" {
				return "", false, nil
			}
			return text, true, nil
		}
	}

	sectionKeys := []string{
		"preamble",
		"tools",
		"rules",
		"docs",
		"addendum",
		"project_context",
		"skills",
		"cwd",
	}
	sections := make([]*durable.PromptSection, 0, len(sectionKeys))
	for _, key := range sectionKeys {
		// BuildSystemPromptSections already wraps non-preamble sections with XML tags, so Tag is false.
		sections = append(sections, durable.Section(key, renderSection(key), false))
	}

	return durable.DefineExtension(&durable.Extension{
		Name:     DurablePromptExtensionName,
		Sections: sections,
	})
}

// FormatDurableInstructions joins custom instructions cleanly for durable agent configuration.
func FormatDurableInstructions(base, extra *string) *string {
	var parts []string
	if base != nil && strings.TrimSpace(*base) != "" {
		parts = append(parts, strings.TrimSpace(*base))
	}
	if extra != nil && strings.TrimSpace(*extra) != "" {
		parts = append(parts, strings.TrimSpace(*extra))
	}
	if len(parts) == 0 {
		return nil
	}
	joined := fmt.Sprintf("%s", strings.Join(parts, "\n\n"))
	return &joined
}

// promptExtractMessageText returns concatenated text blocks from a transcript ai.Message.
func promptExtractMessageText(msg any) string {
	switch m := msg.(type) {
	case *ai.UserMessage:
		if m == nil {
			return ""
		}
		if m.Content.Text != nil {
			return *m.Content.Text
		}
		return promptJoinTextContent(m.Content.Blocks)
	case *ai.AssistantMessage:
		if m == nil {
			return ""
		}
		return promptJoinTextContent(m.Content)
	case *ai.ToolResultMessage:
		if m == nil {
			return ""
		}
		return promptJoinTextContent(m.Content)
	default:
		return ""
	}
}

func promptJoinTextContent(blocks ai.ContentList) string {
	var parts []string
	for _, b := range blocks {
		if tc, ok := b.(*ai.TextContent); ok && tc != nil && tc.Text != "" {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}
