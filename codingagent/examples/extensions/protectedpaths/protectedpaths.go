// Ported from packages/coding-agent/examples/extensions/protected-paths.ts (pi v1.0.0).

package protectedpaths

import (
	"context"
	"fmt"
	"strings"

	"github.com/keejkrej/pi-go/codingagent/core"
)

// Extension blocks write and edit calls targeting protected path fragments.
func Extension(pi core.ExtensionAPI) error {
	protected := []string{".env", ".git/", "node_modules/"}
	pi.OnToolCall(func(_ context.Context, event *core.ToolCallEvent, ectx core.ExtensionContext) (*core.ToolCallEventResult, error) {
		if (event.ToolName != "write" && event.ToolName != "edit") || event.Input == nil {
			return nil, nil
		}
		p, _ := event.Input.GetString("path")
		for _, frag := range protected {
			if strings.Contains(p, frag) {
				if ectx.HasUI() {
					ectx.UI().Notify(fmt.Sprintf("Blocked write to protected path: %s", p), "warning")
				}
				t := true
				reason := fmt.Sprintf("Path %q is protected", p)
				return &core.ToolCallEventResult{Block: &t, Reason: &reason}, nil
			}
		}
		return nil, nil
	})
	return nil
}
