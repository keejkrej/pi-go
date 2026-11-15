// Ported from packages/durable/test/examples/10-registry-reload.ts (pi v1.0.0).

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

type e1rrArgs struct {
	Path string `json:"path"`
}

func e1rrExampleTool(name, description string) *durable.ToolRegistration {
	props := omap.NewMap[string, *typebox.Schema]()
	props.Set("path", typebox.String())
	return durable.DefineTool(&durable.ToolRegistration{
		Name:        name,
		Description: description,
		Parameters:  typebox.Object(props),
		Execute: func(args any, _ durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
			var in e1rrArgs
			if err := jsonx.Decode(args, &in); err != nil {
				return nil, err
			}
			return &durable.ToolExecutionResult{
				Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: name + " " + in.Path}},
				HasContent: true,
			}, nil
		},
	})
}

// Example10RegistryReload runs the 10-registry-reload example.
func Example10RegistryReload(out io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	ctx := TestContext
	read := e1rrExampleTool("read", "Read a file")
	registry := durable.CreateRegistry()
	if err := registry.Install(durable.DefineExtension(&durable.Extension{
		Name:  "files",
		Tools: []*durable.ToolRegistration{read, e1rrExampleTool("grep", "Search files")},
	})); err != nil {
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

	root, err := harness.Root(ctx)
	if err != nil {
		return err
	}
	tools := func() ([]string, error) {
		agent, err := root.Agent(ctx)
		if err != nil {
			return nil, err
		}
		outTools := make([]string, len(agent.Tools))
		for i, tool := range agent.Tools {
			outTools[i] = tool.Name + ": " + tool.Description
		}
		return outTools, nil
	}

	if err := registry.Install(durable.DefineExtension(&durable.Extension{
		Name:  "files",
		Tools: []*durable.ToolRegistration{read, e1rrExampleTool("grep", "Search files, faster")},
	})); err != nil {
		return err
	}
	afterReload, err := tools()
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "after reload: %v\n", afterReload)

	audit := durable.DefineExtension(&durable.Extension{
		Name: "audit",
		Wraps: []durable.Wrap{
			durable.WrapTool(read, func(tool *durable.ToolRegistration) *durable.ToolRegistration {
				cp := *tool
				cp.Description = tool.Description + " (audited)"
				return &cp
			}),
		},
	})
	if err := registry.Install(audit); err != nil {
		return err
	}
	withAudit, err := tools()
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "with audit: %v\n", withAudit)

	registry.Uninstall(audit)
	afterUninstall, err := tools()
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "after uninstall: %v\n", afterUninstall)
	return harness.Close(ctx)
}
