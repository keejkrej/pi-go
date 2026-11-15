// Ported from packages/coding-agent/test/test-network-env.ts (pi v1.5.2).

package testkit

import (
	"os"
	"testing"
)

// AllowNetwork enables network code paths for tests that replace external I/O with local fixtures or mocks
// by temporarily unsetting PI_OFFLINE for the duration of tb.
func AllowNetwork(tb testing.TB) {
	tb.Helper()
	prev, hadPrev := os.LookupEnv("PI_OFFLINE")
	if err := os.Unsetenv("PI_OFFLINE"); err != nil {
		tb.Fatalf("AllowNetwork unset PI_OFFLINE: %v", err)
	}
	tb.Cleanup(func() {
		if hadPrev {
			_ = os.Setenv("PI_OFFLINE", prev)
		} else {
			_ = os.Unsetenv("PI_OFFLINE")
		}
	})
}
