//go:build !darwin && !linux && !windows

// Ported from packages/tui/src/native-platform.ts (pi v1.0.0).

package tui

func npPlatformNewHelper(platform, suffix string) NativeClipboard {
	return nil
}
