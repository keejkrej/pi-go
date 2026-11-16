package hello

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func TestHelloExampleTSAndGo(t *testing.T) {
	ctx := context.Background()

	// 1. Load & execute QuickJS .ts fixture.
	res := core.LoadExtensions(ctx, []string{filepath.Join(".", "hello.ts")}, ".")
	if len(res.Errors) != 0 {
		t.Fatalf("LoadExtensions(hello.ts) errors: %+v", res.Errors)
	}
	tsRunner := core.NewExtensionRunner(res.Extensions, res.Runtime, ".", nil, nil)
	defer tsRunner.Shutdown()

	tsDef := tsRunner.GetToolDefinition("hello")
	if tsDef == nil {
		t.Fatalf("hello tool not registered from hello.ts")
	}
	params := jsonx.ObjectOf("name", "QuickJS")
	tsOut, err := tsDef.Execute(ctx, "c1", params, nil, tsRunner.CreateToolContext("c1", ctx))
	if err != nil {
		t.Fatalf("hello.ts execute failed: %v", err)
	}
	if len(tsOut.Content) == 0 || tsOut.Content[0].(*ai.TextContent).Text != "Hello, QuickJS!" {
		t.Fatalf("unexpected hello.ts output: %+v", tsOut)
	}

	// 2. Load & execute native Go Extension.
	goExt, err := core.LoadExtensionFromFactory(ctx, core.InlineExtension{Name: "hello-go", Factory: Extension}, ".")
	if err != nil {
		t.Fatalf("LoadExtensionFromFactory failed: %v", err)
	}
	goRunner := core.NewExtensionRunner([]core.Extension{*goExt}, nil, ".", nil, nil)
	defer goRunner.Shutdown()

	goDef := goRunner.GetToolDefinition("hello")
	goOut, err := goDef.Execute(ctx, "c2", jsonx.ObjectOf("name", "Go"), nil, goRunner.CreateToolContext("c2", ctx))
	if err != nil {
		t.Fatalf("hello.go execute failed: %v", err)
	}
	if len(goOut.Content) == 0 || goOut.Content[0].(*ai.TextContent).Text != "Hello, Go!" {
		t.Fatalf("unexpected hello.go output: %+v", goOut)
	}
}
