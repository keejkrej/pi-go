// Ported from packages/durable/src/tools/index.ts (pi v1.0.0).

package tools

import (
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

// CodingTools registers the built-in read, write, edit, and bash tools under the "coding-tools" extension.
var CodingTools = durable.DefineExtension(&durable.Extension{
	Name:  "coding-tools",
	Tools: []*durable.ToolRegistration{CreateReadTool(), CreateWriteTool(), CreateEditTool(), CreateBashTool()},
})

type indexProp struct {
	key    string
	schema *typebox.Schema
}

func indexObjectSchema(props ...indexProp) *typebox.Schema {
	m := omap.NewMap[string, *typebox.Schema]()
	for _, p := range props {
		m.Set(p.key, p.schema)
	}
	return typebox.Object(m)
}

func indexIsAborted(ctx chord.Context) bool {
	return ctx != nil && ctx.AbortSignal() != nil && ctx.AbortSignal().Err() != nil
}

func indexDecodeArgs[T any](args any) (T, error) {
	if typed, ok := args.(T); ok {
		return typed, nil
	}
	if ptr, ok := args.(*T); ok && ptr != nil {
		return *ptr, nil
	}
	var zero T
	if err := jsonx.Decode(args, &zero); err != nil {
		return zero, err
	}
	return zero, nil
}
