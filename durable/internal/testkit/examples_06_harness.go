// Ported from packages/durable/test/examples/06-harness.ts (pi v1.0.0).

package testkit

import (
	"fmt"
	"io"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/storage"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

type e0hReadArgs struct {
	Path string `json:"path"`
}

// Example06Harness runs the 06-harness example.
func Example06Harness(out io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	ctx := TestContext
	props := omap.NewMap[string, *typebox.Schema]()
	props.Set("path", typebox.String())
	read := durable.DefineTool(&durable.ToolRegistration{
		Name:        "read",
		Description: "Read a file",
		Parameters:  typebox.Object(props),
		Execute: func(args any, _ durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
			var in e0hReadArgs
			if err := jsonx.Decode(args, &in); err != nil {
				return nil, err
			}
			return &durable.ToolExecutionResult{
				Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: "contents of " + in.Path}},
				HasContent: true,
			}, nil
		},
	})
	files := durable.DefineExtension(&durable.Extension{Name: "files", Tools: []*durable.ToolRegistration{read}})
	registry := durable.CreateRegistry()
	if err := registry.Install(files); err != nil {
		return err
	}

	harness, err := durable.HarnessOpen(storage.NewMemoryStorage(), durable.HarnessOptions{
		Models:   ai.CreateModels(nil),
		Registry: registry,
	}, ctx)
	if err != nil {
		return err
	}
	defer func() { _ = harness.Close(ctx) }()

	notes, err := durable.DefineDoc(durable.DocDefinition{
		Kind:    "example.notes",
		Version: 1,
		Scope:   durable.DocumentScopeConversation,
		History: durable.DocumentHistoryRewindable,
		Fork:    durable.DocumentForkAsOf,
		Initial: func() durable.JsonObject { return jsonx.ObjectOf("text", "") },
	})
	if err != nil {
		return err
	}

	root, err := harness.Root(ctx, durable.RootOptions{
		Agent: &durable.AgentChange{ThinkingLevel: jsonx.Some(ai.ModelThinkingLevelLow)},
		Init: func(tx durable.Tx, rootId durable.ConversationId) error {
			d, err := tx.Doc(notes, rootId)
			if err != nil {
				return err
			}
			d.Value.Set("text", "root notes")
			return nil
		},
	})
	if err != nil {
		return err
	}

	notesSnap, err := harness.Snapshot(ctx, notes, root.Id())
	if err != nil {
		return err
	}
	agentSnap, err := harness.Snapshot(ctx, durable.AgentDoc, root.Id())
	if err != nil {
		return err
	}
	agent, err := root.Agent(ctx)
	if err != nil {
		return err
	}
	extNames := make([]string, len(agent.Extensions))
	for i, ext := range agent.Extensions {
		extNames[i] = ext.Name
	}
	toolNames := make([]string, len(agent.Tools))
	for i, tool := range agent.Tools {
		toolNames[i] = tool.Name
	}
	_, _ = fmt.Fprintf(out, "root: %d %v\nstored agent: %v\nresolved: %s %v %v\n",
		root.Id(), notesSnap, agentSnap, agent.ThinkingLevel, extNames, toolNames)
	return harness.Close(ctx)
}
