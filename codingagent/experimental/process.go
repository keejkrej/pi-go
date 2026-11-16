// Ported from packages/coding-agent/src/experimental/process.ts (pi v1.0.0).

package experimental

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

// InternalProcessEnv holds the environment variable naming the spawned internal Pi role.
const InternalProcessEnv = "__PI_INTERNAL_SPAWN"

// InternalProcessArgsEnv optionally transports exact argument slices for self-re-exec spawns (e.g., inside `go test`).
const InternalProcessArgsEnv = "__PI_INTERNAL_SPAWN_ARGS"

// MaxControlLineBytes caps a single newline-delimited internal control protocol frame.
const MaxControlLineBytes = 128 * 1024 * 1024

// InternalProcessRole identifies one Pi-owned internal child process role.
type InternalProcessRole string

const (
	InternalRoleCoordinator   InternalProcessRole = "coordinator"
	InternalRoleServer        InternalProcessRole = "server"
	InternalRoleSessionWorker InternalProcessRole = "session-worker"
)

// IsValid checks whether r is a supported internal process role.
func (r InternalProcessRole) IsValid() bool {
	return r == InternalRoleCoordinator || r == InternalRoleServer || r == InternalRoleSessionWorker
}

// GetInternalProcessRole reads and validates the internal process role without consuming it.
func GetInternalProcessRole() (InternalProcessRole, bool, error) {
	raw, ok := os.LookupEnv(InternalProcessEnv)
	if !ok {
		return "", false, nil
	}
	role := InternalProcessRole(raw)
	if !role.IsValid() {
		return "", false, fmt.Errorf("Unsupported internal process role: %s", raw)
	}
	return role, true, nil
}

// ConsumeInternalProcessRole reads, validates, and removes the internal process role so descendants do not inherit it.
func ConsumeInternalProcessRole() (InternalProcessRole, bool, error) {
	role, ok, err := GetInternalProcessRole()
	_ = os.Unsetenv(InternalProcessEnv)
	return role, ok, err
}

// ConsumeInternalProcessArgs consumes explicit arguments passed via InternalProcessArgsEnv, falling back to fallbackArgs.
func ConsumeInternalProcessArgs(fallbackArgs []string) ([]string, error) {
	raw, ok := os.LookupEnv(InternalProcessArgsEnv)
	_ = os.Unsetenv(InternalProcessArgsEnv)
	if !ok || raw == "" {
		cp := make([]string, len(fallbackArgs))
		copy(cp, fallbackArgs)
		return cp, nil
	}
	var args []string
	if err := jsonx.Unmarshal([]byte(raw), &args); err != nil {
		return nil, fmt.Errorf("invalid internal process args: %w", err)
	}
	return args, nil
}

// IsDirectInternalProcessEntry reports whether execPath matches expectedEntry after resolving paths.
func IsDirectInternalProcessEntry(argv0 string, expectedEntry string) bool {
	if argv0 == "" || expectedEntry == "" {
		return false
	}
	left, err1 := filepath.Abs(argv0)
	right, err2 := filepath.Abs(expectedEntry)
	if err1 != nil || err2 != nil {
		return false
	}
	return left == right
}

// InternalProcessSpawnOptions configures SpawnInternalProcess.
type InternalProcessSpawnOptions struct {
	Executable    string
	Env           map[string]string
	PassArgsInEnv bool
}

// InternalChildProcess wraps a spawned Go subprocess with exit notification and safe termination.
type InternalChildProcess struct {
	mu       sync.Mutex
	cmd      *exec.Cmd
	pid      int
	done     chan struct{}
	exitErr  error
	exited   bool
	waitOnce sync.Once
}

// Pid returns the OS process ID of the spawned child, or 0 if not started.
func (c *InternalChildProcess) Pid() int {
	if c == nil {
		return 0
	}
	return c.pid
}

// Done is closed when the child process exits.
func (c *InternalChildProcess) Done() <-chan struct{} {
	return c.done
}

// Exited reports whether the child process has terminated.
func (c *InternalChildProcess) Exited() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.exited
}

// ExitError returns any wait error recorded after termination.
func (c *InternalChildProcess) ExitError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.exitErr
}

// Wait blocks until the child process exits and returns its exit error, if any.
func (c *InternalChildProcess) Wait() error {
	<-c.done
	return c.ExitError()
}

// SpawnInternalProcess launches a detached Go internal child process configured for role.
func SpawnInternalProcess(role InternalProcessRole, args []string, options *InternalProcessSpawnOptions) (*InternalChildProcess, error) {
	if !role.IsValid() {
		return nil, fmt.Errorf("Unsupported internal process role: %s", role)
	}
	var overrideExec string
	var extraEnv map[string]string
	var passArgsInEnv bool
	if options != nil {
		overrideExec = options.Executable
		extraEnv = options.Env
		passArgsInEnv = options.PassArgsInEnv
	}
	execPath, err := ResolveExperimentalExecutable(overrideExec)
	if err != nil {
		return nil, err
	}

	cmdArgs := args
	if passArgsInEnv {
		cmdArgs = nil
	}
	cmd := exec.Command(execPath, cmdArgs...)
	if wd, wdErr := os.Getwd(); wdErr == nil {
		cmd.Dir = wd
	}

	env := os.Environ()
	for k, v := range extraEnv {
		env = append(env, k+"="+v)
	}
	env = append(env, string(InternalProcessEnv)+"="+string(role))
	if passArgsInEnv {
		encodedArgs, encErr := jsonx.Marshal(args)
		if encErr != nil {
			return nil, encErr
		}
		env = append(env, InternalProcessArgsEnv+"="+string(encodedArgs))
	} else {
		// Ensure inherited args env does not shadow explicit CLI args.
		env = append(env, InternalProcessArgsEnv+"=")
	}
	cmd.Env = env

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	child := &InternalChildProcess{
		cmd:  cmd,
		pid:  cmd.Process.Pid,
		done: make(chan struct{}),
	}
	go func() {
		waitErr := cmd.Wait()
		child.mu.Lock()
		child.exitErr = waitErr
		child.exited = true
		child.mu.Unlock()
		close(child.done)
	}()
	return child, nil
}

// TerminateInternalProcess forcefully kills child and waits until it exits.
func TerminateInternalProcess(child *InternalChildProcess) error {
	if child == nil || child.cmd == nil || child.cmd.Process == nil {
		return nil
	}
	if child.Exited() {
		return nil
	}
	_ = child.cmd.Process.Kill()
	<-child.done
	return nil
}

// EncodeControlLine serializes a control protocol message as newline-delimited JSON.
func EncodeControlLine(message any) ([]byte, error) {
	encoded, err := jsonx.Marshal(message)
	if err != nil {
		return nil, err
	}
	line := append(encoded, '\n')
	if len(line) > MaxControlLineBytes {
		return nil, fmt.Errorf("Internal control message is too large")
	}
	return line, nil
}

// InternalProcessRunner executes an internal child process role with args.
type InternalProcessRunner func(role InternalProcessRole, args []string) error

// RunInternalProcess dispatches role to its built-in Go entry point ("coordinator", "server", "session-worker").
func RunInternalProcess(role InternalProcessRole, args []string) error {
	switch role {
	case InternalRoleCoordinator:
		return RunCoordinatorProcess(args)
	case InternalRoleServer:
		return RunServerProcess(args)
	case InternalRoleSessionWorker:
		return RunSessionWorkerProcess(args)
	default:
		return fmt.Errorf("Unsupported internal process role: %s", role)
	}
}

// MaybeRunInternalProcess consumes any pending internal spawn role and executes it if present.
func MaybeRunInternalProcess(cliArgs []string) (handled bool, err error) {
	role, ok, roleErr := ConsumeInternalProcessRole()
	if roleErr != nil {
		return true, roleErr
	}
	if !ok {
		return false, nil
	}
	args, argsErr := ConsumeInternalProcessArgs(cliArgs)
	if argsErr != nil {
		return true, argsErr
	}
	return true, RunInternalProcess(role, args)
}
