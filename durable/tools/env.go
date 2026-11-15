// Ported from packages/durable/src/tools/env.ts (pi v1.0.0).

package tools

import (
	"errors"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/env"
)

// RequireEnv returns the call's execution environment, or an error if none is configured.
func RequireEnv(api durable.ToolExecutionApi, ctx chord.Context) (env.ExecutionEnv, error) {
	if api == nil {
		return nil, errors.New("No execution environment is configured")
	}
	executionEnv, err := api.Env(ctx)
	if err != nil {
		return nil, err
	}
	if executionEnv == nil {
		return nil, errors.New("No execution environment is configured")
	}
	return executionEnv, nil
}
