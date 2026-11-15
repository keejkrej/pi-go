// Ported from packages/coding-agent/src/extensions/mcp/cli.lazy.ts (pi v1.5.2).

package mcpext

// McpCommandModule bundles the `pi mcp` CLI command handler loaded on first invocation.
type McpCommandModule struct {
	RunMcpCommand func(args []string, options McpCommandOptions) int
}

// LoadMcpCommand returns the `pi mcp` CLI command module.
func LoadMcpCommand() McpCommandModule {
	return McpCommandModule{
		RunMcpCommand: RunMcpCommand,
	}
}

// RunMcpCli runs `pi mcp <args>` and returns the exit code.
func RunMcpCli(args []string, options ...McpCommandOptions) int {
	var opts McpCommandOptions
	if len(options) > 0 {
		opts = options[0]
	}
	return LoadMcpCommand().RunMcpCommand(args, opts)
}
