//go:build unix

// Ported from packages/tui/src/terminal.ts (pi v1.5.2).

package tui

import (
	"errors"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

func termSendSigwinch(pid int) error {
	if pid <= 0 {
		return nil
	}
	return syscall.Kill(pid, syscall.SIGWINCH)
}

func termEnableWindowsVTInput() (bool, error) {
	return false, nil
}

func termWatchResize(onResize func()) func() {
	if onResize == nil {
		return func() {}
	}
	ch := make(chan os.Signal, 4)
	signal.Notify(ch, syscall.SIGWINCH)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ch:
				onResize()
			case <-done:
				return
			}
		}
	}()
	return func() {
		signal.Stop(ch)
		close(done)
	}
}

func termMakeRaw(f *os.File) (*term.State, bool, error) {
	if f == nil {
		return nil, false, nil
	}
	fd := int(f.Fd())
	if !term.IsTerminal(fd) {
		return nil, false, nil
	}
	wasRaw := false
	if termios, err := unix.IoctlGetTermios(fd, termIoctlGetTermios); err == nil && termios != nil {
		// In canonical mode ICANON is set; non-raw if ICANON != 0.
		wasRaw = (termios.Lflag & unix.ICANON) == 0
	}
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return nil, wasRaw, err
	}
	return oldState, wasRaw, nil
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
	wakeR, wakeW, err := os.Pipe()
	if err != nil {
		return func() {}
	}
	stdinFd := int(f.Fd())
	wakeFd := int(wakeR.Fd())
	done := make(chan struct{})

	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		var pending []byte
		pollFds := []unix.PollFd{
			{Fd: int32(stdinFd), Events: unix.POLLIN},
			{Fd: int32(wakeFd), Events: unix.POLLIN},
		}
		for {
			pollFds[0].Revents = 0
			pollFds[1].Revents = 0
			_, err := unix.Poll(pollFds, -1)
			if err != nil {
				if errors.Is(err, unix.EINTR) {
					continue
				}
				return
			}
			if pollFds[1].Revents != 0 {
				return
			}
			if pollFds[0].Revents&unix.POLLIN != 0 {
				n, readErr := syscall.Read(stdinFd, buf)
				if n > 0 {
					if s := termDecodeUTF8Chunk(buf[:n], &pending); s != "" {
						onData(s)
					}
				}
				if readErr != nil {
					if errors.Is(readErr, unix.EINTR) || errors.Is(readErr, unix.EAGAIN) {
						continue
					}
					return
				}
				if n == 0 {
					return
				}
				continue
			}
			if pollFds[0].Revents&(unix.POLLHUP|unix.POLLERR|unix.POLLNVAL) != 0 {
				return
			}
		}
	}()

	return func() {
		_ = wakeW.Close()
		<-done
		_ = wakeR.Close()
	}
}

func termDrainStdinDirect(f *os.File, maxMs, idleMs int64) {
	if f == nil {
		return
	}
	fd := int(f.Fd())
	if !term.IsTerminal(fd) {
		return
	}
	buf := make([]byte, 4096)
	lastDataTime := time.Now()
	endTime := lastDataTime.Add(time.Duration(maxMs) * time.Millisecond)
	idleDur := time.Duration(idleMs) * time.Millisecond
	pollFds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}

	for {
		now := time.Now()
		timeLeft := endTime.Sub(now)
		if timeLeft <= 0 {
			break
		}
		if now.Sub(lastDataTime) >= idleDur {
			break
		}
		waitDur := idleDur
		if timeLeft < waitDur {
			waitDur = timeLeft
		}
		waitMs := int(waitDur / time.Millisecond)
		if waitMs < 1 {
			waitMs = 1
		}
		pollFds[0].Revents = 0
		n, err := unix.Poll(pollFds, waitMs)
		if err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			break
		}
		if n > 0 && pollFds[0].Revents&unix.POLLIN != 0 {
			readN, readErr := syscall.Read(fd, buf)
			if readN > 0 {
				lastDataTime = time.Now()
			} else if readErr != nil && !errors.Is(readErr, unix.EINTR) {
				break
			}
		}
	}
}
