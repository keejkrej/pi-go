//go:build !unix

// Ported from packages/evals/src/harness.ts (pi v1.0.0).

package evals

import "fmt"

func harnPlatformSandboxSupported() bool { return false }

// HarnessChownTree recursively sets ownership on root and all descendants to uid:gid.
func HarnessChownTree(string, int, int) error {
	return fmt.Errorf("The eval filesystem sandbox requires POSIX user APIs.")
}

// HarnessAssertRootOnly checks that path is owned by uid 0 with no group or world permission bits.
func HarnessAssertRootOnly(string) error {
	return fmt.Errorf("The eval filesystem sandbox requires POSIX user APIs.")
}

func harnEnterToolSandbox(_ string, identity *harnSandboxIdentity) error {
	if identity == nil {
		return nil
	}
	return fmt.Errorf("The eval filesystem sandbox requires POSIX user APIs.")
}
