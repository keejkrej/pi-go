// Ported from packages/durable/src/harness/define.ts (pi v1.0.0).

package durable

import (
	"github.com/keejkrej/pi-go/chord"
)

// DefineExtension returns extension unchanged; Go counterpart of defineExtension().
func DefineExtension(extension *Extension) *Extension {
	return extension
}

// DefineTool returns tool unchanged; Go counterpart of defineTool().
func DefineTool(tool *ToolRegistration) *ToolRegistration {
	return tool
}

// Section builds a PromptSection; tagged unless tag is explicitly passed as false.
func Section(key string, render PromptRenderFn, tag ...bool) *PromptSection {
	s := &PromptSection{
		Key:    key,
		Render: render,
	}
	if len(tag) > 0 {
		v := tag[0]
		s.Tag = &v
	}
	return s
}

// Hook builds a HookRegistration matching tasks with task's name.
func Hook[I, S, R, H any](task *Task[I, S, R, H], handlers H) HookRegistration {
	return HookRegistration{
		Task:     task.erasedDefinition().Name,
		Handlers: handlers,
	}
}

// WrapTool builds a Wrap targeting tools named like tool.
func WrapTool(tool *ToolRegistration, wrapper ToolWrapFn) Wrap {
	return Wrap{
		Tool:     tool.Name,
		WrapTool: wrapper,
	}
}

// WrapSection builds a Wrap targeting the section named key.
func WrapSection(key string, wrapper SectionWrapFn) Wrap {
	return Wrap{
		Section:     key,
		WrapSection: wrapper,
	}
}

// AgentExtensionsSet creates an AgentExtensionsChange replacing extensions with list (even when empty).
func AgentExtensionsSet(extensions ...*Extension) AgentExtensionsChange {
	if extensions == nil {
		extensions = []*Extension{}
	}
	return AgentExtensionsChange{List: extensions}
}

// AgentExtensionsEdit creates an AgentExtensionsChange editing host default extensions via add and remove.
func AgentExtensionsEdit(add []*Extension, remove []*Extension) AgentExtensionsChange {
	return AgentExtensionsChange{Add: add, Remove: remove}
}

// AgentToolsSet creates an AgentToolsChange replacing tools with list (even when empty).
func AgentToolsSet(tools ...*ToolRegistration) AgentToolsChange {
	if tools == nil {
		tools = []*ToolRegistration{}
	}
	return AgentToolsChange{List: tools}
}

// AgentToolsRemove creates an AgentToolsChange filtering out remove.
func AgentToolsRemove(tools ...*ToolRegistration) AgentToolsChange {
	return AgentToolsChange{Remove: tools}
}

// SimplePromptSection creates an untagged or tagged literal PromptSection helper.
func SimplePromptSection(key string, text string, tag ...bool) *PromptSection {
	return Section(key, func(_ PromptInput, _ chord.Context) (string, bool, error) {
		return text, true, nil
	}, tag...)
}
