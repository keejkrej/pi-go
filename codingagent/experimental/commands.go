// Ported from packages/coding-agent/src/experimental/commands.ts (pi v1.0.0).

package experimental

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/keejkrej/pi-go/codingagent/cli/experimentalcli"
)

// CliCommandOptions configures NewCliContext, RunServerCommand, and RunClientCommand.
type CliCommandOptions struct {
	Context   context.Context
	Directory string
	Stdout    io.Writer
	Stderr    io.Writer
	Fallback  func(ctx context.Context, args []string) int
}

type commCliContext struct {
	ctx       context.Context
	directory string
	stdout    io.Writer
	stderr    io.Writer
}

// NewCliContext returns an experimentalcli.CliContext wired to foreground server and client runners.
func NewCliContext(options ...CliCommandOptions) experimentalcli.CliContext {
	opts := commNormalizeCommandOptions(options)
	return &commCliContext{
		ctx:       opts.Context,
		directory: opts.Directory,
		stdout:    opts.Stdout,
		stderr:    opts.Stderr,
	}
}

func (c *commCliContext) RunServer(command experimentalcli.ServerCommand) error {
	return RunServerCommand(command, CliCommandOptions{
		Context:   c.ctx,
		Directory: c.directory,
		Stdout:    c.stdout,
		Stderr:    c.stderr,
	})
}

func (c *commCliContext) RunClient(command experimentalcli.ClientCommand) error {
	return RunClientCommand(command, CliCommandOptions{
		Context:   c.ctx,
		Directory: c.directory,
		Stdout:    c.stdout,
		Stderr:    c.stderr,
	})
}

// RunServerCommand starts a foreground experimental server for the parsed CLI command and waits until shutdown.
func RunServerCommand(command experimentalcli.ServerCommand, options ...CliCommandOptions) error {
	opts := commNormalizeCommandOptions(options)

	var pluginPackages *[]string
	if command.PluginPackages != nil {
		resolved := make([]string, 0, len(command.PluginPackages))
		for _, pkgPath := range command.PluginPackages {
			abs, err := filepath.Abs(pkgPath)
			if err != nil {
				return err
			}
			resolved = append(resolved, abs)
		}
		pluginPackages = &resolved
	}

	var outMu sync.Mutex
	writeOut := func(format string, args ...any) {
		outMu.Lock()
		defer outMu.Unlock()
		_, _ = fmt.Fprintf(opts.Stdout, format, args...)
	}

	startOpts := StartServerOptions{
		Directory:      opts.Directory,
		ServerId:       command.ServerId,
		RelayAuth:      command.Auth,
		PluginPackages: pluginPackages,
		OnRelayStatus: func(status RadiusRelayHostStatus) {
			if status.Error != "" {
				writeOut("Radius relay (%s): %s\n", status.Status, status.Error)
			} else if status.Status != "" {
				writeOut("Radius relay: %s\n", status.Status)
			}
		},
	}
	if command.SessionDir != nil {
		startOpts.SessionDir = *command.SessionDir
	}
	if command.Provider != nil {
		startOpts.Provider = *command.Provider
	}
	if command.Model != nil {
		startOpts.Model = *command.Model
	}

	running, err := StartForegroundServer(startOpts)
	if err != nil {
		return err
	}
	writeOut("Experimental server listening: %s (%s)\n", running.SocketPath, running.ServerId)

	select {
	case <-opts.Context.Done():
		if err := running.Close(); err != nil {
			return err
		}
		return running.CloseErr()
	case <-running.Closed():
		return running.CloseErr()
	}
}

// RunClientCommand connects to discovered experimental servers and runs either a one-shot prompt or interactive TUI.
func RunClientCommand(command experimentalcli.ClientCommand, options ...CliCommandOptions) error {
	opts := commNormalizeCommandOptions(options)

	if command.Prompt != nil {
		res, err := RunClient(command, RunClientOptions{Directory: opts.Directory})
		if err != nil {
			return err
		}
		if res.Text != "" {
			_, _ = fmt.Fprintln(opts.Stdout, res.Text)
		}
		return nil
	}

	runtime, err := OpenClientRuntime(command, OpenClientRuntimeOptions{Directory: opts.Directory})
	if err != nil {
		return err
	}
	defer func() { _ = runtime.Dispose() }()

	servers := make([]ClientTuiServer, 0, len(runtime.Servers))
	for _, srv := range runtime.Servers {
		servers = append(servers, ClientTuiServer{
			ServerId: srv.Route.ServerId,
			Radius:   srv.Route.Transport == "radius",
			Server:   srv.Server,
			Session:  srv.Session,
		})
	}

	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	return RunClientTui(opts.Context, ClientTuiOptions{
		Command: command,
		Servers: servers,
		Cwd:     cwd,
	})
}

func commNormalizeCommandOptions(options []CliCommandOptions) CliCommandOptions {
	var opts CliCommandOptions
	if len(options) > 0 {
		opts = options[0]
	}
	if opts.Context == nil {
		opts.Context = context.Background()
	}
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	return opts
}
