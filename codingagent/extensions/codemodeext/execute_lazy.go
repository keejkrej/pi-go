// Ported from packages/coding-agent/src/extensions/codemode/execute.lazy.ts (pi v1.0.0).

package codemodeext

import (
	"context"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/codingagent/core"
)

// CodemodeExecutor executes one codemode script inside the sandbox runtime.
type CodemodeExecutor func(
	ctx context.Context,
	toolCallId string,
	input CodemodeToolInput,
	onUpdate agent.AgentToolUpdateCallback,
	ectx core.ExtensionToolContext,
	options ...*CodemodeToolOptions,
) (agent.AgentToolResult, error)

// LoadCodemodeExecutor returns the codemode execution entry point.
// Unlike TypeScript dynamic import(), Go links execute.go directly at compile time.
func LoadCodemodeExecutor() CodemodeExecutor {
	return ExecuteCodemode
}
