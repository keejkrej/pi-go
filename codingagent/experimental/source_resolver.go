// Ported from packages/coding-agent/src/experimental/source-resolver.ts (pi v1.0.0).

package experimental

import (
	"os"
	"path/filepath"
	"strings"
)

// ResolveExperimentalExecutable resolves the binary path used when spawning internal Go worker processes.
// In Go binaries (including `go test` test binaries), os.Executable() re-executes the compiled executable
// directly without Node/Bun runtime dependencies or TypeScript source-alias hooks.
func ResolveExperimentalExecutable(override string) (string, error) {
	if trimmed := strings.TrimSpace(override); trimmed != "" {
		abs, err := filepath.Abs(trimmed)
		if err != nil {
			return "", err
		}
		return abs, nil
	}
	return os.Executable()
}
