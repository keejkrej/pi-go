// Ported from packages/durable/src/storage/jsonl/node.ts (pi v1.0.0).

package jsonl

import (
	"os"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable/env"
)

// OpenNodeJsonlStorage opens or creates a JSONL storage directory using the local host filesystem.
func OpenNodeJsonlStorage(directory string, ctx chord.Context, options ...JsonlStorageOptions) (*JsonlStorage, error) {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	execEnv := env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{Cwd: cwd})
	return OpenJsonlStorage(directory, execEnv, ctx, options...)
}
