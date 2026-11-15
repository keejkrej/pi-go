package withdeps

import (
	"context"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func TestWithDepsPackageDiscoveryAndToolTS(t *testing.T) {
	ctx := context.Background()

	// Load directory containing package.json -> pi.extensions: ["index.ts"]
	res := core.LoadExtensions(ctx, []string{"."}, ".")
	if len(res.Errors) != 0 {
		t.Fatalf("LoadExtensions(.) errors: %+v", res.Errors)
	}
	runner := core.NewExtensionRunner(res.Extensions, res.Runtime, ".", nil, nil)
	defer runner.Shutdown()

	def := runner.GetToolDefinition("parse_duration")
	if def == nil {
		t.Fatalf("expected parse_duration tool registered")
	}
	out, err := def.Execute(ctx, "d1", jsonx.ObjectOf("duration", "2 days"), nil, runner.CreateToolContext("d1", ctx))
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	got := out.Content[0].(*ai.TextContent).Text
	if !strings.Contains(got, "172800000 milliseconds") {
		t.Fatalf("unexpected duration result: %q", got)
	}
}
