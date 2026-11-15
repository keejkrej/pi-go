//go:build windows

// Ported from packages/tui/src/terminal.ts (pi v1.5.2).

package tui

import (
	"os"
	"sync/atomic"

	"golang.org/x/sys/windows"
	"golang.org/x/term"
)

func termSendSigwinch(pid int) error {
	return nil
}

func termEnableWindowsVTInput() (bool, error) {
	return npEnableVirtualTerminalInput()
}

func termWatchResize(onResize func()) func() {
	return func() {}
}

func termMakeRaw(f *os.File) (*term.State, bool, error) {
	if f == nil {
		return nil, false, nil
	}
	fd := int(f.Fd())
	if !term.IsTerminal(fd) {
		return nil, false, nil
	}
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return nil, false, err
	}
	return oldState, false, nil
}

func termRestoreRaw(f *os.File, state *term.State) {
	if f == nil || state == nil {
		return
	}
	_ = term.Restore(int(f.Fd()), state)
}

func termGetWindowSize(f *os.File) (cols, rows int, ok bool) {
	if f == nil {
		return 0, 0, false
	}
	fd := int(f.Fd())
	if !term.IsTerminal(fd) {
		return 0, 0, false
	}
	c, r, err := term.GetSize(fd)
	if err != nil || c <= 0 || r <= 0 {
		return 0, 0, false
	}
	return c, r, true
}

func termIsStdinTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

func termStartStdinReader(f *os.File, onData func(string)) func() {
	if f == nil || onData == nil {
		return func() {}
	}
	var stopped atomic.Bool
	done := make(chan struct{})
	handle := windows.Handle(f.Fd())

	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		var pending []byte
		for !stopped.Load() {
			n, err := f.Read(buf)
			if stopped.Load() {
				return
			}
			if n > 0 {
				if s := termDecodeUTF8Chunk(buf[:n], &pending); s != "" {
					onData(s)
				}
			}
			if err != nil || n == 0 {
				return
			}
		}
	}()

	return func() {
		stopped.Store(true)
		_ = windows.CancelIoEx(handle, nil)
		<-done
	}
}

func termDrainStdinDirect(f *os.File, maxMs, idleMs int64) {
	// Windows console draining happens via ProcessTerminal's active reader/sleep loop.
}
