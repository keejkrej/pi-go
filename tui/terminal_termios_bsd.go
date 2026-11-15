//go:build darwin || freebsd || openbsd || netbsd || dragonfly

// Ported from packages/tui/src/terminal.ts (pi v1.5.2).

package tui

import "golang.org/x/sys/unix"

const termIoctlGetTermios = unix.TIOCGETA
