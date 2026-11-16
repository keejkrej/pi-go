//go:build unix

// Ported from packages/evals/src/harness.ts (pi v1.0.0).

package evals

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func harnPlatformSandboxSupported() bool { return true }

// HarnessChownTree recursively sets ownership on root and all descendants to uid:gid.
func HarnessChownTree(path string, uid, gid int) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
		entries, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := HarnessChownTree(filepath.Join(path, entry.Name()), uid, gid); err != nil {
				return err
			}
		}
	}
	return os.Lchown(path, uid, gid)
}

// HarnessAssertRootOnly checks that path is owned by uid 0 with no group or world permission bits.
func HarnessAssertRootOnly(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("Cannot inspect ownership metadata for %s.", path)
	}
	if st.Uid != 0 || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("Evaluator source must be readable only by root: %s", path)
	}
	return nil
}

func harnEnterToolSandbox(root string, identity *harnSandboxIdentity) error {
	if identity == nil {
		return nil
	}
	if os.Getuid() != 0 || os.Geteuid() != 0 {
		return fmt.Errorf("The eval runner must start as root before entering the unprivileged tool sandbox.")
	}
	if err := HarnessChownTree(root, identity.uid, identity.gid); err != nil {
		return err
	}
	if err := syscall.Setgroups([]int{}); err != nil {
		return err
	}
	if err := syscall.Setgid(identity.gid); err != nil {
		return err
	}
	if err := syscall.Setuid(identity.uid); err != nil {
		return err
	}
	if os.Getuid() != identity.uid || os.Geteuid() != identity.uid {
		return fmt.Errorf("Failed to enter the unprivileged eval tool sandbox.")
	}
	return nil
}
