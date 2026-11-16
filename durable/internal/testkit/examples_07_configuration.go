// Ported from packages/durable/test/examples/07-configuration.ts (pi v1.0.0).

package testkit

import (
	"fmt"
	"io"
	"strings"
	"sync/atomic"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/storage"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

type e0coArgs struct {
	Path string `json:"path"`
}

func e0coExampleTool(name, description string, snippets map[*durable.ToolRegistration]string) *durable.ToolRegistration {
	props := omap.NewMap[string, *typebox.Schema]()
	props.Set("path", typebox.String())
	tool := durable.DefineTool(&durable.ToolRegistration{
		Name:        name,
		Description: description,
		Parameters:  typebox.Object(props),
		Execute: func(args any, _ durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
			var in e0coArgs
			if err := jsonx.Decode(args, &in); err != nil {
				return nil, err
			}
			return &durable.ToolExecutionResult{
				Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: name + " " + in.Path}},
				HasContent: true,
			}, nil
		},
	})
	snippets[tool] = fmt.Sprintf("Use %s for files.", name)
	return tool
}

// Example07Configuration runs the 07-configuration example.
func Example07Configuration(out io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	ctx := TestContext
	snippets := map[*durable.ToolRegistration]string{}
	read := e0coExampleTool("read", "Read a file", snippets)
	write := e0coExampleTool("write", "Write a file", snippets)
	grep := e0coExampleTool("grep", "Search files", snippets)

	files := durable.DefineExtension(&durable.Extension{Name: "files", Tools: []*durable.ToolRegistration{read, write}})
	search := durable.DefineExtension(&durable.Extension{Name: "search", Tools: []*durable.ToolRegistration{grep}})
	snippetsExt := durable.DefineExtension(&durable.Extension{
		Name: "snippets",
		Sections: []*durable.PromptSection{
			durable.Section("tool_snippets", func(input durable.PromptInput, _ chord.Context) (string, bool, error) {
				var lines []string
				for _, tool := range input.Agent.Tools {
					if s, ok := snippets[tool]; ok {
						lines = append(lines, s)
					}
				}
				return strings.Join(lines, "\n"), true, nil
			}),
		},
	})

	registry := durable.CreateRegistry()
	for _, ext := range []*durable.Extension{files, search, snippetsExt} {
		if err := registry.Install(ext); err != nil {
			return err
		}
	}

	var timeoutMs atomic.Int64
	timeoutMs.Store(60_000)
	maxRetries := 5
	harness, err := durable.HarnessOpen(storage.NewMemoryStorage(), durable.HarnessOptions{
		Models:   ai.CreateModels(nil),
		Registry: registry,
		Settings: func() durable.HarnessSettings {
			ms := timeoutMs.Load()
			return durable.HarnessSettings{
				Stream:        &durable.ConversationStreamOptions{TimeoutMs: &ms},
				Retry:         &durable.ConversationRetryPolicyPatch{MaxRetries: &maxRetries},
				ToolExecution: durable.ToolExecutionModeSequential,
			}
		},
	}, ctx)
	if err != nil {
		return err
	}
	defer func() { _ = harness.Close(ctx) }()

	root, err := harness.Root(ctx)
	if err != nil {
		return err
	}
	toolNames := func() ([]string, error) {
		agent, err := root.Agent(ctx)
		if err != nil {
			return nil, err
		}
		names := make([]string, len(agent.Tools))
		for i, tool := range agent.Tools {
			names[i] = tool.Name
		}
		return names, nil
	}

	t0, err := toolNames()
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "default tools: %v\n", t0)

	if err := root.Configure(durable.AgentChange{
		Model:         jsonx.Some(durable.ModelRef{Provider: "anthropic", ModelId: "claude-sonnet-4-5"}),
		ThinkingLevel: jsonx.Some(ai.ModelThinkingLevelHigh),
		Tools:         jsonx.Some(durable.AgentToolsSet(write, read)),
	}, ctx); err != nil {
		return err
	}
	stored, err := harness.Snapshot(ctx, durable.AgentDoc, root.Id())
	if err != nil {
		return err
	}
	agent, err := root.Agent(ctx)
	if err != nil {
		return err
	}
	t1, err := toolNames()
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "stored: %v\nmodel: %+v thinking: %s tools: %v\n", stored, agent.Model, agent.ThinkingLevel, t1)

	if err := root.Configure(durable.AgentChange{
		Extensions: jsonx.Some(durable.AgentExtensionsEdit(nil, []*durable.Extension{search})),
		Tools:      jsonx.Null[durable.AgentToolsChange](),
	}, ctx); err != nil {
		return err
	}
	t2, err := toolNames()
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "without search: %v\n", t2)

	if err := root.Configure(durable.AgentChange{
		Extensions: jsonx.Some(durable.AgentExtensionsSet(files, search)),
	}, ctx); err != nil {
		return err
	}
	registry.Uninstall(files)
	t3, err := toolNames()
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "files uninstalled: %v\n", t3)
	if err := registry.Install(files); err != nil {
		return err
	}
	t4, err := toolNames()
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "files reinstalled: %v\n", t4)

	timeoutMs.Store(120_000)
	return harness.Close(ctx)
}
