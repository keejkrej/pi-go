// Ported from packages/coding-agent/examples/extensions/permission-gate.ts (pi v1.0.0).

package permissiongate

import (
	"context"
	"fmt"
	"regexp"

	"github.com/keejkrej/pi-go/codingagent/core"
)

var dangerousPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\brm\s+(-rf?|--recursive)`),
	regexp.MustCompile(`(?i)\bsudo\b`),
	regexp.MustCompile(`(?i)\b(chmod|chown)\b.*777`),
}

// Extension gates dangerous bash commands behind interactive confirmation (or blocks in headless mode).
func Extension(pi core.ExtensionAPI) error {
	pi.OnToolCall(func(ctx context.Context, event *core.ToolCallEvent, ectx core.ExtensionContext) (*core.ToolCallEventResult, error) {
		if event.ToolName != "bash" || event.Input == nil {
			return nil, nil
		}
		command, _ := event.Input.GetString("command")
		dangerous := false
		for _, re := range dangerousPatterns {
			if re.MatchString(command) {
				dangerous = true
				break
			}
		}
		if !dangerous {
			return nil, nil
		}
		t := true
		if !ectx.HasUI() {
			reason := "Dangerous command blocked (no UI for confirmation)"
			return &core.ToolCallEventResult{Block: &t, Reason: &reason}, nil
		}
		choice, err := ectx.UI().Select(ctx, fmt.Sprintf("⚠️ Dangerous command:\n\n  %s\n\nAllow?", command), []string{"Yes", "No"})
		if err != nil || choice == nil || *choice != "Yes" {
			reason := "Blocked by user"
			return &core.ToolCallEventResult{Block: &t, Reason: &reason}, nil
		}
		return nil, nil
	})
	return nil
}
