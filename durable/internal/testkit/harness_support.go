// Ported from packages/durable/test/harness-support.ts (pi v1.0.0).

package testkit

import (
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

var tkhsHookExtensions atomic.Int64

// Tool builds a no-op ToolRegistration with an empty object schema for harness tests.
func Tool(name string, description ...string) *durable.ToolRegistration {
	desc := fmt.Sprintf("%s tool", name)
	if len(description) > 0 && description[0] != "" {
		desc = description[0]
	}
	return durable.DefineTool(&durable.ToolRegistration{
		Name:        name,
		Description: desc,
		Parameters:  typebox.Object(omap.NewMap[string, *typebox.Schema]()),
		Execute: func(_ any, _ durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
			return &durable.ToolExecutionResult{Content: ai.ContentList{}, HasContent: true}, nil
		},
	})
}

// OpenHarnessOptions configures OpenHarness.
type OpenHarnessOptions struct {
	Registry durable.Registry
	OnReport func(error)
}

// OpenHarness opens a Harness over storage with a registry holding the named placeholder tools.
func OpenHarness(st durable.Storage, toolNames []string, opts ...OpenHarnessOptions) (durable.Harness, durable.Registry, error) {
	var opt OpenHarnessOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	registry := opt.Registry
	if registry == nil {
		registry = durable.CreateRegistry()
	}
	if len(toolNames) > 0 {
		tools := make([]*durable.ToolRegistration, 0, len(toolNames))
		for _, name := range toolNames {
			tools = append(tools, Tool(name))
		}
		if err := registry.Install(durable.DefineExtension(&durable.Extension{
			Name:  "tools",
			Tools: tools,
		})); err != nil {
			return nil, nil, err
		}
	}
	harness, err := durable.HarnessOpen(st, durable.HarnessOptions{
		Models:   ai.CreateModels(nil),
		Registry: registry,
		OnReport: opt.OnReport,
	}, TestContext)
	if err != nil {
		return nil, nil, err
	}
	return harness, registry, nil
}

// User builds a minimal UserMessage for harness view/context tests.
func User(text string) *ai.UserMessage {
	t := text
	return &ai.UserMessage{
		Role:      "user",
		Content:   ai.UserMessageContent{Text: &t},
		Timestamp: 1,
	}
}

// AssistantOptions configures Assistant test messages.
type AssistantOptions struct {
	Calls      []string
	StopReason *ai.StopReason
}

// Assistant builds a minimal AssistantMessage with optional tool calls for harness tests.
func Assistant(text string, opts ...AssistantOptions) *ai.AssistantMessage {
	var opt AssistantOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	content := ai.ContentList{
		&ai.TextContent{Type: "text", Text: text},
	}
	for _, id := range opt.Calls {
		content = append(content, &ai.ToolCall{
			Type:      "toolCall",
			Id:        id,
			Name:      "tool-" + id,
			Arguments: jsonx.NewObject(),
		})
	}
	stopReason := ai.StopReasonStop
	if opt.StopReason != nil {
		stopReason = *opt.StopReason
	} else if len(opt.Calls) > 0 {
		stopReason = ai.StopReasonToolUse
	}
	return &ai.AssistantMessage{
		Role:       "assistant",
		Content:    content,
		Api:        "faux",
		Provider:   "faux",
		Model:      "faux",
		StopReason: stopReason,
		Timestamp:  2,
	}
}

// ToolResult builds a minimal ToolResultMessage for harness tests.
func ToolResult(id string, text ...string) *ai.ToolResultMessage {
	t := fmt.Sprintf("result %s", id)
	if len(text) > 0 {
		t = text[0]
	}
	return &ai.ToolResultMessage{
		Role:       "toolResult",
		ToolCallId: id,
		ToolName:   "tool-" + id,
		Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: t}},
		IsError:    false,
		Timestamp:  3,
	}
}

// System builds a minimal SystemMessage carrying rendered sections for harness tests.
func System(sections *omap.Map[string, *string]) *ai.SystemMessage {
	empty := ""
	return &ai.SystemMessage{
		Role:      "system",
		Content:   ai.SystemMessageContent{Text: &empty},
		Sections:  sections,
		Timestamp: 4,
	}
}

// DescribeMessage renders an ai.Message concisely for harness test assertions.
func DescribeMessage(message ai.Message) string {
	switch m := message.(type) {
	case *ai.UserMessage:
		if m.Content.Text != nil {
			return "user:" + *m.Content.Text
		}
		return "user:"
	case *ai.AssistantMessage:
		for _, block := range m.Content {
			if tc, ok := block.(*ai.TextContent); ok {
				return "assistant:" + tc.Text
			}
		}
		return "assistant:"
	case *ai.ToolResultMessage:
		if m.IsError {
			return fmt.Sprintf("result:%s:error", m.ToolCallId)
		}
		for _, block := range m.Content {
			if tc, ok := block.(*ai.TextContent); ok {
				return fmt.Sprintf("result:%s:%s", m.ToolCallId, tc.Text)
			}
		}
		return fmt.Sprintf("result:%s:", m.ToolCallId)
	case *ai.SystemMessage:
		if m.Sections == nil {
			return "system:"
		}
		return "system:" + strings.Join(m.Sections.Keys(), ",")
	default:
		return ""
	}
}

// Installed uninstalls a helper-installed extension from its registry.
type Installed struct {
	dispose func()
}

// Dispose uninstalls the installed extension.
func (i Installed) Dispose() {
	if i.dispose != nil {
		i.dispose()
	}
}

func tkhsInstallOne(registry durable.Registry, extension *durable.Extension) (Installed, error) {
	if err := registry.Install(extension); err != nil {
		return Installed{}, err
	}
	return Installed{dispose: func() { registry.Uninstall(extension) }}, nil
}

// AddTool installs a one-tool extension named after tool.
func AddTool(registry durable.Registry, tool *durable.ToolRegistration, name ...string) (Installed, error) {
	extName := "tool:" + tool.Name
	if len(name) > 0 && name[0] != "" {
		extName = name[0]
	}
	return tkhsInstallOne(registry, durable.DefineExtension(&durable.Extension{
		Name:  extName,
		Tools: []*durable.ToolRegistration{tool},
	}))
}

// AddTask installs a one-task extension named after task.
func AddTask(registry durable.Registry, task durable.AnyTask, name ...string) (Installed, error) {
	extName := "task:" + task.TaskKind()
	if len(name) > 0 && name[0] != "" {
		extName = name[0]
	}
	return tkhsInstallOne(registry, durable.DefineExtension(&durable.Extension{
		Name:  extName,
		Tasks: []durable.AnyTask{task},
	}))
}

// AddHooks installs an extension containing one hook registration for task.
func AddHooks[I, S, R, H any](registry durable.Registry, task *durable.Task[I, S, R, H], handlers H, name ...string) (Installed, error) {
	extName := fmt.Sprintf("hooks:%d", tkhsHookExtensions.Add(1))
	if len(name) > 0 && name[0] != "" {
		extName = name[0]
	}
	return tkhsInstallOne(registry, durable.DefineExtension(&durable.Extension{
		Name:  extName,
		Hooks: []durable.HookRegistration{durable.Hook(task, handlers)},
	}))
}

// AddSectionOptions configures AddSection.
type AddSectionOptions struct {
	Tag  *bool
	Name string
}

// AddSection installs a one-section extension named after key.
func AddSection(registry durable.Registry, key string, render durable.PromptRenderFn, opts ...AddSectionOptions) (Installed, error) {
	var opt AddSectionOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	extName := "section:" + key
	if opt.Name != "" {
		extName = opt.Name
	}
	var sec *durable.PromptSection
	if opt.Tag != nil {
		sec = durable.Section(key, render, *opt.Tag)
	} else {
		sec = durable.Section(key, render)
	}
	return tkhsInstallOne(registry, durable.DefineExtension(&durable.Extension{
		Name:     extName,
		Sections: []*durable.PromptSection{sec},
	}))
}
