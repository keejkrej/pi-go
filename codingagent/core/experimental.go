// Ported from packages/coding-agent/src/core/experimental.ts (pi v1.5.2).

package core

import "os"

// AreExperimentalFeaturesEnabled reports whether PI_EXPERIMENTAL=1 is set in the environment.
func AreExperimentalFeaturesEnabled() bool {
	return os.Getenv("PI_EXPERIMENTAL") == "1"
}
