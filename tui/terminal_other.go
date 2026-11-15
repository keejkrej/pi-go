//go:build !unix && !windows

// Ported from packages/tui/src/terminal.ts (pi v1.5.2).

package tui

import (
	"os"

	"golang.org/x/term"
)

func termSendSigwinch(pid int) error {
	return nil
}

func termEnableWindowsVTInput() (bool, error) {
	return false, nil
}

func termWatchResize(onResize func()) func() {
	return func() {}
}

func termMakeRaw(f *os.File) (*term.State, bool, error) {
	return nil, false, nil
}

func termRestoreRaw(f *os.File, state *term.State) {}

func termGetWindowSize(f *os.File) (cols, rows int, ok bool) {
	return 0, 0, false
}

func termIsStdinTerminal(f *os.File) bool {
	return false
}

func termStartStdinReader(f *os.File, onData func(string)) func() {
	return func() {}
}

func termDrainStdinDirect(f *os.File, maxMs, idleMs int64) {}
