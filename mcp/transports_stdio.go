// Ported from packages/mcp/src/transports/stdio.ts (pi v1.0.0).

package mcp

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/xspawn"
	"github.com/keejkrej/pi-go/mcp/protocol"
)

const (
	tsDefaultMaxStderrBytes       = 64 * 1024
	tsDefaultCloseTimeoutMs int64 = 2_000
	tsStdinCloseGraceMs     int64 = 500
)

// tsUseProcessGroups reports whether servers run in their own process group.
// Windows has no graceful signals and does not use process groups.
func tsUseProcessGroups() bool { return js.Platform() != "win32" }

// Process groups of running servers, killed if the host exits without closing them.
var (
	tsLiveProcessGroups = omap.NewSet[int]()
	tsExitHookInstalled bool
	tsLiveMu            sync.Mutex
)

func tsKillProcessTree(child *exec.Cmd, signal string) {
	tsPlatformKillProcessTree(child, signal)
}

func tsInstallExitHook() {
	// Go libraries do not intercept process-wide SIGINT/SIGTERM handlers.
	tsExitHookInstalled = true
}

// StdioTransportStderr is StdioTransportOptions.stderr.
type StdioTransportStderr string

const (
	StdioTransportStderrPipe    StdioTransportStderr = "pipe"
	StdioTransportStderrInherit StdioTransportStderr = "inherit"
)

// StdioTransportOptions configures a spawned MCP server.
type StdioTransportOptions struct {
	Command string
	Args    []string
	Cwd     *string
	Env     *omap.Map[string, string]
	// InheritEnv, when non-nil and false, replaces the process environment
	// instead of extending it. Nil means inherit.
	InheritEnv      *bool
	Stderr          *StdioTransportStderr
	OnStderr        func(chunk string)
	MaxMessageBytes *int
	MaxStderrBytes  *int
	// CloseTimeoutMs is how long to wait for the server to exit after SIGTERM
	// before sending SIGKILL. Nil means 2000.
	CloseTimeoutMs *int64
}

// StdioTransport speaks JSON-RPC with a child process over stdin and stdout.
type StdioTransport struct {
	TransportEvents
	mu           sync.Mutex
	writeMu      sync.Mutex
	Options      StdioTransportOptions
	lines        *JsonRpcLineReader
	child        *exec.Cmd
	stdin        *os.File
	stdoutBuffer []byte
	stderrBuffer []byte
	started      bool
	exited       bool
	closed       bool
	doneCh       chan struct{}
}

// NewStdioTransport returns a transport that has not been started.
func NewStdioTransport(options *StdioTransportOptions) *StdioTransport {
	var opts StdioTransportOptions
	if options != nil {
		opts = *options
	}
	t := &StdioTransport{Options: opts}
	t.lines = NewJsonRpcLineReader(JsonRpcLineReaderOptions{
		MaxMessageBytes: opts.MaxMessageBytes,
		MaxStderrBytes:  opts.MaxStderrBytes,
		OnStderr:        opts.OnStderr,
		OnMessage:       func(msg protocol.JsonRpcMessage) { t.emitMessage(msg) },
		OnError:         func(err error) { t.emitError(err) },
	})
	return t
}

// Pid is the child process id, or nil before start and after exit.
func (t *StdioTransport) Pid() *int {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.exited || t.child == nil || t.child.Process == nil {
		return nil
	}
	pid := t.child.Process.Pid
	return &pid
}

// Stderr is the retained stderr text, capped at MaxStderrBytes.
func (t *StdioTransport) Stderr() string {
	if t.lines == nil {
		return ""
	}
	return t.lines.Stderr()
}

func (t *StdioTransport) Start() error {
	t.mu.Lock()
	if t.started {
		t.mu.Unlock()
		return errors.New("Stdio MCP transport already started")
	}
	if t.closed {
		t.mu.Unlock()
		return protocol.NewMcpConnectionClosedError()
	}
	t.started = true
	opts := t.Options
	t.mu.Unlock()

	inheritEnv := true
	if opts.InheritEnv != nil {
		inheritEnv = *opts.InheritEnv
	}
	envSlice := tsBuildEnv(inheritEnv, opts.Env)
	cwd := ""
	if opts.Cwd != nil {
		cwd = *opts.Cwd
	}

	cmd := xspawn.CommandIn(context.Background(), cwd, envSlice, opts.Command, opts.Args)
	tsConfigureProcessGroup(cmd)

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		return err
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		_ = stdinR.Close()
		_ = stdinW.Close()
		return err
	}
	var stderrR, stderrW *os.File
	stderrInherit := opts.Stderr != nil && *opts.Stderr == StdioTransportStderrInherit
	if !stderrInherit {
		stderrR, stderrW, err = os.Pipe()
		if err != nil {
			_ = stdinR.Close()
			_ = stdinW.Close()
			_ = stdoutR.Close()
			_ = stdoutW.Close()
			return err
		}
	}

	cmd.Stdin = stdinR
	cmd.Stdout = stdoutW
	if stderrInherit {
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stderr = stderrW
	}

	err = cmd.Start()
	_ = stdinR.Close()
	_ = stdoutW.Close()
	if stderrW != nil {
		_ = stderrW.Close()
	}
	if err != nil {
		_ = stdinW.Close()
		_ = stdoutR.Close()
		if stderrR != nil {
			_ = stderrR.Close()
		}
		return xspawn.VerifyENOENT(cmd, err)
	}

	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		_ = stdinW.Close()
		tsKillProcessTree(cmd, "SIGKILL")
		go func() {
			_ = cmd.Wait()
			_ = stdoutR.Close()
			if stderrR != nil {
				_ = stderrR.Close()
			}
		}()
		return protocol.NewMcpConnectionClosedError()
	}
	done := make(chan struct{})
	t.child = cmd
	t.stdin = stdinW
	t.doneCh = done
	pgid := 0
	if tsUseProcessGroups() && cmd.Process != nil {
		pgid = cmd.Process.Pid
	}
	t.mu.Unlock()

	if pgid > 0 {
		tsLiveMu.Lock()
		tsLiveProcessGroups.Add(pgid)
		tsInstallExitHook()
		tsLiveMu.Unlock()
	}

	stdoutDone := make(chan struct{})
	go func() {
		defer close(stdoutDone)
		defer func() { _ = stdoutR.Close() }()
		buf := make([]byte, 32*1024)
		for {
			n, rerr := stdoutR.Read(buf)
			if n > 0 {
				t.handleStdout(buf[:n])
			}
			if rerr != nil {
				if rerr != io.EOF && !errors.Is(rerr, os.ErrClosed) {
					t.mu.Lock()
					closed := t.closed
					t.mu.Unlock()
					if !closed {
						t.emitError(rerr)
					}
				}
				return
			}
		}
	}()

	var stderrDone chan struct{}
	if stderrR != nil {
		stderrDone = make(chan struct{})
		go func() {
			defer close(stderrDone)
			defer func() { _ = stderrR.Close() }()
			buf := make([]byte, 32*1024)
			for {
				n, rerr := stderrR.Read(buf)
				if n > 0 {
					t.handleStderr(buf[:n])
				}
				if rerr != nil {
					if rerr != io.EOF && !errors.Is(rerr, os.ErrClosed) {
						t.mu.Lock()
						closed := t.closed
						t.mu.Unlock()
						if !closed {
							t.emitError(rerr)
						}
					}
					return
				}
			}
		}()
	}

	go func() {
		waitErr := cmd.Wait()
		var notFoundErr *xspawn.NotFoundError
		if vErr := xspawn.VerifyENOENT(cmd, waitErr); errors.As(vErr, &notFoundErr) {
			t.mu.Lock()
			closed := t.closed
			t.mu.Unlock()
			if !closed {
				t.emitError(notFoundErr)
			}
		}

		t.mu.Lock()
		t.exited = true
		t.mu.Unlock()

		<-stdoutDone
		if stderrDone != nil {
			<-stderrDone
		}

		if pgid > 0 {
			tsLiveMu.Lock()
			tsLiveProcessGroups.Delete(pgid)
			tsLiveMu.Unlock()
		}

		t.mu.Lock()
		t.child = nil
		stdin := t.stdin
		t.stdin = nil
		t.mu.Unlock()
		if stdin != nil {
			_ = stdin.Close()
		}

		t.lines.End()
		t.emitClose()
		close(done)
	}()

	return nil
}

func (t *StdioTransport) Send(message protocol.JsonRpcMessage) error {
	raw, err := jsonx.Marshal(message)
	if err != nil {
		return err
	}
	payload := append(raw, '\n')

	t.writeMu.Lock()
	defer t.writeMu.Unlock()

	t.mu.Lock()
	if !t.started || t.closed || t.exited || t.stdin == nil {
		t.mu.Unlock()
		return protocol.NewMcpConnectionClosedError()
	}
	stdin := t.stdin
	t.mu.Unlock()

	for len(payload) > 0 {
		n, err := stdin.Write(payload)
		if err != nil {
			return err
		}
		payload = payload[n:]
	}
	return nil
}

func (t *StdioTransport) Close() error {
	t.mu.Lock()
	if t.closed {
		done := t.doneCh
		t.mu.Unlock()
		if done != nil {
			<-done
		}
		return nil
	}
	t.closed = true
	cmd := t.child
	stdin := t.stdin
	t.stdin = nil
	exited := t.exited
	done := t.doneCh
	closeTimeoutMs := tsDefaultCloseTimeoutMs
	if t.Options.CloseTimeoutMs != nil {
		closeTimeoutMs = *t.Options.CloseTimeoutMs
	}
	t.mu.Unlock()

	if cmd == nil {
		t.emitClose()
		return nil
	}

	if stdin != nil {
		_ = stdin.Close()
	}

	if !tsUseProcessGroups() && exited {
		if done != nil {
			<-done
		}
		return nil
	}

	graceMs := min(tsStdinCloseGraceMs, closeTimeoutMs)
	if graceMs < 0 {
		graceMs = 0
	}
	if closeTimeoutMs < 0 {
		closeTimeoutMs = 0
	}

	graceTimer := time.NewTimer(time.Duration(graceMs) * time.Millisecond)
	defer graceTimer.Stop()

	select {
	case <-done:
		tsKillProcessTree(cmd, "SIGTERM")
		return nil
	case <-graceTimer.C:
	}

	tsKillProcessTree(cmd, "SIGTERM")

	killTimer := time.NewTimer(time.Duration(closeTimeoutMs) * time.Millisecond)
	defer killTimer.Stop()

	select {
	case <-done:
		tsKillProcessTree(cmd, "SIGTERM")
		return nil
	case <-killTimer.C:
	}

	tsKillProcessTree(cmd, "SIGKILL")
	<-done
	return nil
}

func (t *StdioTransport) handleStdout(chunk []byte) {
	if t.lines != nil {
		t.lines.ReadStdout(chunk)
	}
}

func (t *StdioTransport) handleStderr(chunk []byte) {
	if t.lines != nil {
		t.lines.ReadStderr(chunk)
	}
}

func tsBuildEnv(inherit bool, overrides *omap.Map[string, string]) []string {
	hasOverrides := overrides != nil && overrides.Len() > 0
	if inherit && !hasOverrides {
		return nil
	}
	if !inherit && !hasOverrides {
		return []string{}
	}
	envMap := omap.NewMap[string, string]()
	if inherit {
		for _, entry := range os.Environ() {
			k, v, ok := strings.Cut(entry, "=")
			if ok {
				envMap.Set(k, v)
			}
		}
	}
	if hasOverrides {
		for k, v := range overrides.All() {
			envMap.Set(k, v)
		}
	}
	out := make([]string, 0, envMap.Len())
	for k, v := range envMap.All() {
		out = append(out, k+"="+v)
	}
	return out
}

var _ McpTransport = (*StdioTransport)(nil)
