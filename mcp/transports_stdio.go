// Ported from packages/mcp/src/transports/stdio.ts (pi v1.0.0).

package mcp

import (
	"os/exec"
	"sync"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/omap"
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
	panic("unported: tsKillProcessTree")
}

func tsInstallExitHook() {
	panic("unported: tsInstallExitHook")
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
	Options      StdioTransportOptions
	child        *exec.Cmd
	stdoutBuffer []byte
	stderrBuffer []byte
	started      bool
	closed       bool
}

// NewStdioTransport returns a transport that has not been started.
func NewStdioTransport(options *StdioTransportOptions) *StdioTransport {
	panic("unported: NewStdioTransport")
}

// Pid is the child process id, or nil before start and after exit.
func (t *StdioTransport) Pid() *int {
	panic("unported: StdioTransport.Pid")
}

// Stderr is the retained stderr text, capped at MaxStderrBytes.
func (t *StdioTransport) Stderr() string {
	panic("unported: StdioTransport.Stderr")
}

func (t *StdioTransport) Start() error {
	panic("unported: StdioTransport.Start")
}

func (t *StdioTransport) Send(message protocol.JsonRpcMessage) error {
	panic("unported: StdioTransport.Send")
}

func (t *StdioTransport) Close() error {
	panic("unported: StdioTransport.Close")
}

func (t *StdioTransport) handleStdout(chunk []byte) {
	panic("unported: StdioTransport.handleStdout")
}

func (t *StdioTransport) handleStderr(chunk []byte) {
	panic("unported: StdioTransport.handleStderr")
}

var _ McpTransport = (*StdioTransport)(nil)
