package customfooter

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/core"
)

func TestCustomFooterExampleLoadsTS(t *testing.T) {
	ctx := context.Background()

	res := core.LoadExtensions(ctx, []string{filepath.Join(".", "custom-footer.ts")}, ".")
	if len(res.Errors) != 0 {
		t.Fatalf("LoadExtensions(custom-footer.ts) errors: %+v", res.Errors)
	}
	runner := core.NewExtensionRunner(res.Extensions, res.Runtime, ".", nil, nil)
	defer runner.Shutdown()

	if cmd := runner.GetCommand("footer"); cmd == nil {
		t.Fatalf("expected /footer command registered from custom-footer.ts")
	}
}
